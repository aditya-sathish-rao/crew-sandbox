// The guest's only way out. A CONNECT proxy that refuses every destination the
// allowlist does not name, and logs every decision it makes.
//
// It holds no credentials for the allowlist route and terminates no TLS there: it
// forwards bytes, so a compromised guest gains nothing beyond the hosts named in
// the allowlist. The one exception is the model gateway below, which exists so the
// real model key can stay on the host.
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import https from "node:https";
import net from "node:net";

const argv = process.argv.slice(2);
const flag = (name, fallback) => {
  const at = argv.indexOf(name);
  return at >= 0 ? argv[at + 1] : fallback;
};

const port = Number(flag("--listen", "3128"));
const allowFile = flag("--allow");
const modelUpstream = flag("--model-upstream", "https://api.deepseek.com");
const keyHashOut = flag("--key-hash-out");
const selftest = argv.includes("--selftest");
// The selftest exercises the parser alone, so it needs no allowlist and can run
// anywhere node is.
if (!allowFile && !selftest) {
  console.error("crew-proxy: --allow <file> is required");
  process.exit(2);
}

// The model key and the guest's token are independent: the key comes from the
// vault when the proxy starts, the token is created inside the VM. Without both,
// the gateway is disabled rather than open.
const modelKey = process.env.DEEPSEEK_API_KEY;
const modelToken = process.env.CREW_MODEL_TOKEN;
const modelReady = Boolean(modelKey && modelToken);
if (keyHashOut) {
  const hash = modelKey ? crypto.createHash("sha256").update(modelKey).digest("hex") : "";
  fs.writeFileSync(keyHashOut, `${hash}\n`);
}

const rules = allowFile
  ? fs
      .readFileSync(allowFile, "utf8")
      .split("\n")
      .map((line) => line.split("#")[0].trim())
      .filter(Boolean)
  : [];

const isAllowedHost = (hostname) => {
  const host = hostname.toLowerCase().replace(/\.$/, "");
  return rules.some((rule) =>
    rule.startsWith(".") ? host === rule.slice(1) || host.endsWith(rule) : host === rule,
  );
};

// Two ports, because that is all a package manager or a git remote ever needs.
// Anything else is a service the guest has no business reaching.
const isAllowedPort = (portNumber) => portNumber === 443 || portNumber === 80;

const log = (decision, target, from) =>
  console.error(`${new Date().toISOString()} ${decision} ${target} from ${from}`);

const splitTarget = (authority) => {
  const match = /^([a-z0-9.-]+)(?::(\d+))?$/i.exec(authority);
  if (!match) return undefined;
  return { hostname: match[1], port: Number(match[2] ?? 443) };
};

const deny = (socket, decision, target, from) => {
  log(decision, target, from);
  socket.end("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n");
};

// A CONNECT tunnel only names its destination. Everything that decides which
// virtual host answers is inside the TLS handshake, so an allowlisted address is
// not enough on its own: a guest could tunnel to an allowlisted CDN address and
// ask it for any other site it hosts. The first flight is the ClientHello, and
// its server_name extension is the one name the peer's certificate will be
// checked against, so requiring SNI to equal the CONNECT name is what makes the
// allowlist describe a host rather than an IP. With that equality, a wildcard
// rule no longer widens the tunnel: the name inside must still be the name the
// guest asked for.
//
// Returns { status: "need" } until enough bytes exist, { status: "no", reason }
// when the flight is not a readable ClientHello, or { status: "ok", sni } with
// the server_name (null when the client offered none).
const parseClientHelloSni = (buffer) => {
  const need = (at, length) => buffer.length >= at + length;
  const no = (reason) => ({ status: "no", reason });
  if (!need(0, 5)) return { status: "need" };
  if (buffer[0] !== 0x16) return no("not-tls");
  const recordLength = buffer.readUInt16BE(3);
  if (recordLength > 16384) return no("record-too-large");
  if (!need(5, recordLength)) return { status: "need" };
  if (buffer[5] !== 0x01) return no("not-client-hello");
  const helloLength = buffer.readUIntBE(6, 3);
  // A ClientHello split across records is legal but vanishingly rare, and
  // reassembling it is not worth the attack surface. Refuse rather than guess.
  if (helloLength > recordLength - 4) return no("hello-split");
  const end = 9 + helloLength;
  if (!need(0, end)) return { status: "need" };

  let at = 9 + 2 + 32; // legacy_version + random
  if (!need(at, 1)) return no("truncated");
  at += 1 + buffer[at]; // session_id
  if (at > end) return no("truncated");
  if (!need(at, 2)) return no("truncated");
  at += 2 + buffer.readUInt16BE(at); // cipher_suites
  if (at > end) return no("truncated");
  if (!need(at, 1)) return no("truncated");
  at += 1 + buffer[at]; // compression_methods
  if (at > end) return no("truncated");
  if (!need(at, 2)) return no("truncated");
  // The declared extension block must fit inside the ClientHello. Without this
  // clamp a body that lies about its own length reaches past the available
  // bytes and throws out of a socket handler, which takes the whole proxy down.
  const extensionsEnd = at + 2 + buffer.readUInt16BE(at);
  if (extensionsEnd > end) return no("extensions-overrun");
  at += 2;

  while (at + 4 <= extensionsEnd) {
    const type = buffer.readUInt16BE(at);
    const length = buffer.readUInt16BE(at + 2);
    const body = at + 4;
    if (body + length > extensionsEnd) return no("extension-overrun");
    if (type === 0x0000) {
      if (length < 5) return no("sni-malformed");
      const nameType = buffer[body + 2];
      const nameLength = buffer.readUInt16BE(body + 3);
      if (nameType !== 0 || nameLength === 0 || body + 5 + nameLength > body + length) {
        return no("sni-malformed");
      }
      return {
        status: "ok",
        sni: buffer.toString("latin1", body + 5, body + 5 + nameLength),
      };
    }
    at = body + length;
  }
  return { status: "ok", sni: null };
};

// The model gateway. pi's models.json points the deepseek provider at
// http://host.lima.internal:3128/model, so the guest asks this route for
// completions and the real key is added here, in the one host process that has it.
// The route is closed unless both the vault key and the guest's token are present.
const modelPrefix = "/model";
const modelRoutes = new Map([
  ["GET /models", "/models"],
  ["POST /chat/completions", "/chat/completions"],
]);
// RFC 7230 hop-by-hop headers, meaningful to one connection only, so never
// forwarded. `host` is deliberately not here: every route sets it from the
// target, so a guest cannot choose which virtual host an allowlisted address
// serves. Forwarding the guest's Host let a request to an allowlisted host be
// answered by any other site sharing its CDN address.
const hopByHop = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

const stripHopByHop = (headers) => {
  const out = { ...headers };
  if (typeof out.connection === "string") {
    for (const name of out.connection.split(",")) delete out[name.trim().toLowerCase()];
  }
  for (const name of hopByHop) delete out[name];
  return out;
};

const isModelRequest = (url) =>
  url === modelPrefix || url.startsWith(`${modelPrefix}/`) || url.startsWith(`${modelPrefix}?`);

const tokenMatches = (header) => {
  if (!modelToken || typeof header !== "string" || !header.startsWith("Bearer ")) return false;
  const given = Buffer.from(header.slice("Bearer ".length));
  const expected = Buffer.from(modelToken);
  return given.length === expected.length && crypto.timingSafeEqual(given, expected);
};

const replyJson = (response, status, payload) => {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(JSON.stringify(payload));
};

const handleModel = (request, response) => {
  const from = request.socket.remoteAddress;
  const route = new URL(request.url, "http://crew-model");
  const path = route.pathname.slice(modelPrefix.length);
  const label = `${request.method} ${path}`;

  if (!modelReady) {
    log("DENY", `model-disabled ${label}`, from);
    replyJson(response, 503, { error: "model gateway is disabled" });
    return;
  }
  if (!tokenMatches(request.headers.authorization)) {
    log("DENY", `model-auth ${label}`, from);
    replyJson(response, 401, { error: "unauthorized" });
    return;
  }
  const upstreamPath = modelRoutes.get(label);
  if (!upstreamPath) {
    log("DENY", `model-path ${label}`, from);
    replyJson(response, 404, { error: "not found" });
    return;
  }

  const target = new URL(upstreamPath, modelUpstream);
  const headers = stripHopByHop(request.headers);
  headers.host = target.host;
  headers.authorization = `Bearer ${modelKey}`;

  const upstream = https.request(
    target,
    { method: request.method, headers },
    (upstreamResponse) => {
      const responseHeaders = { ...upstreamResponse.headers };
      const responseHopByHop = [
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
      ];
      for (const name of responseHopByHop) delete responseHeaders[name];
      response.writeHead(upstreamResponse.statusCode, responseHeaders);
      upstreamResponse.pipe(response);
    },
  );
  upstream.setTimeout(15 * 60 * 1000, () => upstream.destroy(new Error("upstream timeout")));
  upstream.on("error", () => {
    if (response.headersSent) response.destroy();
    else replyJson(response, 502, { error: "model upstream failed" });
  });
  request.pipe(upstream);
  response.on("close", () => {
    if (!upstream.destroyed) upstream.destroy();
  });
  log("ALLOW", `model ${label}`, from);
};

const server = http.createServer((req, res) => {
  if (isModelRequest(req.url)) {
    handleModel(req, res);
    return;
  }

  const from = req.socket.remoteAddress;
  let target;
  try {
    target = new URL(req.url);
  } catch {
    res.writeHead(400).end();
    return;
  }

  const targetPort = Number(target.port || 80);
  if (
    target.protocol !== "http:" ||
    !isAllowedHost(target.hostname) ||
    !isAllowedPort(targetPort)
  ) {
    log("DENY", `${target.hostname}:${targetPort}`, from);
    res.writeHead(403).end();
    return;
  }

  // Host comes from the target, never from the guest. This route is the only
  // place a plain-HTTP request can be retargeted at a different virtual host on
  // the same CDN address, so the guest's value must not survive.
  const headers = stripHopByHop(req.headers);
  headers.host = target.host;

  log("ALLOW", `${target.hostname}:${targetPort}`, from);
  const upstream = http.request(
    {
      host: target.hostname,
      port: targetPort,
      method: req.method,
      path: `${target.pathname}${target.search}`,
      headers,
    },
    (upstreamResponse) => {
      res.writeHead(upstreamResponse.statusCode, upstreamResponse.headers);
      upstreamResponse.pipe(res);
    },
  );
  upstream.on("error", () => res.writeHead(502).end());
  req.pipe(upstream);
});

// tunnel: hand an already-validated first flight to the upstream and splice.
const tunnel = (target, socket, firstFlight, sni, from) => {
  log("ALLOW", `${target.hostname}:${target.port} sni=${sni}`, from);
  const upstream = net.connect(target.port, target.hostname, () => {
    upstream.write(firstFlight);
    upstream.pipe(socket);
    // pipe() resumes the socket, releasing anything that arrived while the
    // handshake was being read and the tunnel was still being opened.
    socket.pipe(upstream);
  });
  upstream.on("error", () => socket.destroy());
  upstream.on("close", () => socket.destroy());
  socket.on("close", () => upstream.destroy());
};

server.on("connect", (req, socket, head) => {
  const from = req.socket.remoteAddress;
  const target = splitTarget(req.url);
  // CONNECT is TLS only. A tunnel to port 80 would carry plain HTTP, whose Host
  // header is the very thing this route cannot see, so it would reopen the
  // retargeting hole the SNI check below closes. Plain HTTP has its own route,
  // which rewrites Host from the target.
  if (!target || target.port !== 443 || !isAllowedHost(target.hostname)) {
    deny(socket, "DENY", req.url, from);
    return;
  }
  const label = `${target.hostname}:${target.port}`;
  const expected = target.hostname.toLowerCase().replace(/\.$/, "");

  // The client will not send its ClientHello until the tunnel is established, so
  // the 200 has to come first. Nothing is forwarded upstream until the name in
  // that handshake is known to match the name that was allowed, so a mismatched
  // tunnel is refused before a single byte of it reaches the peer.
  socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");

  let buffered = Buffer.from(head);
  let decided = false;

  const refuse = (reason) => {
    if (decided) return;
    decided = true;
    socket.removeListener("data", onData);
    log("DENY", `tls ${label} ${reason}`, from);
    socket.destroy();
  };

  const onData = (chunk) => {
    if (decided) return;
    if (chunk.length > 0) buffered = Buffer.concat([buffered, chunk]);
    if (buffered.length > 65536) return refuse("hello-too-large");
    let hello;
    try {
      hello = parseClientHelloSni(buffered);
    } catch {
      // A malformed flight must cost one tunnel, never the process.
      return refuse("malformed");
    }
    if (hello.status === "need") return;
    if (hello.status === "no") return refuse(hello.reason);
    if (!hello.sni) return refuse("no-sni");
    const named = hello.sni.toLowerCase().replace(/\.$/, "");
    if (named !== expected) return refuse(`sni-mismatch ${named}`);
    decided = true;
    // Pause before detaching the listener: a flowing stream with no `data`
    // handler discards what arrives, so anything the client sent after the
    // ClientHello would be lost. Paused, those bytes stay buffered and pipe()
    // resumes the stream into the upstream.
    socket.pause();
    socket.removeListener("data", onData);
    tunnel(target, socket, buffered, named, from);
  };

  socket.on("data", onData);
  socket.on("error", () => socket.destroy());
  onData(Buffer.alloc(0));
});

// --selftest: prove the ClientHello parser without a network or a VM. The
// handshake is built here byte by byte, so a wrong offset or a missed bounds
// check fails the build rather than a live tunnel.
if (selftest) {
  const clientHello = (sni, { split = false } = {}) => {
    const name = sni ? Buffer.from(sni, "latin1") : Buffer.alloc(0);
    const sniBody = sni
      ? Buffer.concat([
          Buffer.from([0x00, 0x00]), // server_name list length, patched below
          Buffer.from([0x00]), // host_name
          Buffer.from([(name.length >> 8) & 0xff, name.length & 0xff]),
          name,
        ])
      : Buffer.alloc(0);
    if (sni) sniBody.writeUInt16BE(sniBody.length - 2, 0);
    const extensions = sni
      ? Buffer.concat([
          Buffer.from([0x00, 0x00, (sniBody.length >> 8) & 0xff, sniBody.length & 0xff]),
          sniBody,
        ])
      : Buffer.alloc(0);
    const body = Buffer.concat([
      Buffer.from([0x03, 0x03]), // legacy_version
      Buffer.alloc(32, 0x11), // random
      Buffer.from([0x00]), // session_id length
      Buffer.from([0x00, 0x02, 0x13, 0x01]), // cipher_suites
      Buffer.from([0x01, 0x00]), // compression_methods
      Buffer.from([(extensions.length >> 8) & 0xff, extensions.length & 0xff]),
      extensions,
    ]);
    const hello = Buffer.concat([
      Buffer.from([
        0x01,
        (body.length >> 16) & 0xff,
        (body.length >> 8) & 0xff,
        body.length & 0xff,
      ]),
      body,
    ]);
    const record = Buffer.concat([
      Buffer.from([0x16, 0x03, 0x01, (hello.length >> 8) & 0xff, hello.length & 0xff]),
      hello,
    ]);
    return split ? record.subarray(0, 20) : record;
  };

  const lyingExtensions = Buffer.from(clientHello(null));
  lyingExtensions.writeUInt16BE(0xffff, lyingExtensions.length - 2);

  const cases = [
    [
      "a named host",
      clientHello("registry.npmjs.org"),
      { status: "ok", sni: "registry.npmjs.org" },
    ],
    ["no server_name", clientHello(null), { status: "ok", sni: null }],
    ["a partial record", clientHello("a.example").subarray(0, 30), { status: "need" }],
    ["a split record", clientHello("a.example", { split: true }), { status: "need" }],
    ["plain http", Buffer.from("GET / HTTP/1.1\r\n"), { status: "no", reason: "not-tls" }],
    ["a truncated hello", clientHello("a.example").subarray(0, 12), { status: "need" }],
    [
      "an extension block past the end",
      lyingExtensions,
      { status: "no", reason: "extensions-overrun" },
    ],
  ];
  let ok = true;
  for (const [label, input, want] of cases) {
    const got = parseClientHelloSni(input);
    const match =
      got.status === want.status &&
      (want.sni === undefined || got.sni === want.sni) &&
      (want.reason === undefined || got.reason === want.reason);
    if (!match) ok = false;
    console.error(`selftest: ${match ? "ok  " : "FAIL"} ${label} -> ${JSON.stringify(got)}`);
  }
  process.exit(ok ? 0 : 1);
}

server.listen(port, "127.0.0.1", () => {
  const model = modelReady ? `on ${modelUpstream}` : "disabled (no key or token)";
  console.error(
    `crew-proxy: listening on 127.0.0.1:${port} with ${rules.length} allowed hosts; model gateway ${model}`,
  );
});

for (const signal of ["SIGTERM", "SIGINT"]) {
  process.on(signal, () => {
    server.close();
    process.exit(0);
  });
}
