// The host-side token broker. A crewmate never holds a GitHub credential of its
// own: it asks this for one, gets a token scoped to a single repository for less
// than an hour, and the token is revoked at teardown.
//
// The App installation covers one repository, so a token for anything else is
// refused by GitHub, not by our validation. The allowlist here is the second lock.
import crypto from "node:crypto";
import fs from "node:fs";
import http from "node:http";

const env = process.env;
const port = Number(env.CREW_BROKER_PORT ?? 3129);
const appId = env.CREW_APP_ID;
const privateKey = env.GITHUB_APP_PRIVATE_KEY;
const pinnedInstallation = env.CREW_INSTALLATION_ID;
const reposFile = env.CREW_BROKER_REPOS_FILE;

if (!appId) {
  console.error("crew-broker: CREW_APP_ID is required");
  process.exit(2);
}
if (!privateKey) {
  console.error("crew-broker: GITHUB_APP_PRIVATE_KEY is required (it belongs in the vault)");
  process.exit(2);
}
if (!reposFile) {
  console.error("crew-broker: CREW_BROKER_REPOS_FILE is required; refusing to run without an allowlist");
  process.exit(2);
}

let repos;
try {
  repos = fs
    .readFileSync(reposFile, "utf8")
    .split("\n")
    .map((line) => line.split("#")[0].trim())
    .filter(Boolean);
} catch (error) {
  console.error(`crew-broker: cannot read ${reposFile}: ${error.message}`);
  process.exit(2);
}
if (repos.length === 0) {
  console.error(`crew-broker: ${reposFile} names no repositories; refusing to run with an empty allowlist`);
  process.exit(2);
}

// The fingerprint GitHub shows beside a generated key, computed from the public
// half. Printing it lets an operator confirm the vault holds the key they think it
// does without the key itself ever reaching a terminal.
const keyFingerprint = () =>
  `SHA256:${crypto.createHash("sha256").update(crypto.createPublicKey(privateKey).export({ type: "spki", format: "der" })).digest("base64")}`;

const base64url = (input) =>
  Buffer.from(input).toString("base64").replace(/=+$/, "").replace(/\+/g, "-").replace(/\//g, "_");

// GitHub rejects an App JWT whose iat is in the future and refuses one older than
// ten minutes, so the clock is deliberately skewed back a minute.
const appJwt = () => {
  const now = Math.floor(Date.now() / 1000);
  const header = base64url(JSON.stringify({ alg: "RS256", typ: "JWT" }));
  const payload = base64url(JSON.stringify({ iat: now - 60, exp: now + 540, iss: appId }));
  const signature = crypto.createSign("RSA-SHA256").update(`${header}.${payload}`).sign(privateKey);
  return `${header}.${payload}.${base64url(signature)}`;
};

const api = async (path, { method = "GET", body, authorization } = {}) => {
  const response = await fetch(`https://api.github.com${path}`, {
    method,
    body,
    headers: {
      accept: "application/vnd.github+json",
      "x-github-api-version": "2022-11-28",
      authorization: authorization ?? `Bearer ${appJwt()}`,
    },
  });
  const payload = response.status === 204 ? undefined : await response.json().catch(() => undefined);
  return { status: response.status, payload };
};

let discovered;

const installationId = async () => {
  if (pinnedInstallation) return pinnedInstallation;
  if (discovered) return discovered;
  const { status, payload } = await api("/app/installations");
  if (status !== 200 || !Array.isArray(payload)) {
    throw new Error(`installation lookup failed with ${status}`);
  }
  if (payload.length !== 1) {
    throw new Error(`found ${payload.length} installations; set CREW_INSTALLATION_ID to pick one`);
  }
  discovered = payload[0].id;
  console.error(`crew-broker: installation ${discovered} on ${payload[0].account?.login ?? "?"}`);
  return discovered;
};

const mint = async (repo) => {
  const id = await installationId();
  const { status, payload } = await api(`/app/installations/${id}/access_tokens`, {
    method: "POST",
    body: JSON.stringify({
      repositories: [repo],
      permissions: { contents: "write", pull_requests: "write" },
    }),
  });
  if (status !== 201) {
    throw new Error(`mint for ${repo} failed with ${status}: ${payload?.message ?? ""}`);
  }
  return { token: payload.token, expires_at: payload.expires_at };
};

const revoke = async (token) => {
  const { status } = await api("/installation/token", {
    method: "DELETE",
    authorization: `token ${token}`,
  });
  return status === 204;
};

// task id -> the one token it was given. Memory only: a restart strands every
// token it ever handed out, which is the safe direction to fail in.
const issued = new Map();

const readJson = (request) =>
  new Promise((resolve, reject) => {
    let body = "";
    request.on("data", (chunk) => {
      body += chunk;
      if (body.length > 8192) reject(new Error("body too large"));
    });
    request.on("end", () => {
      try {
        resolve(body ? JSON.parse(body) : {});
      } catch {
        reject(new Error("body is not json"));
      }
    });
  });

const reply = (response, status, payload) => {
  response.writeHead(status, { "content-type": "application/json" });
  response.end(payload === undefined ? "" : JSON.stringify(payload));
};

const server = http.createServer(async (request, response) => {
  const route = new URL(request.url, "http://broker");

  if (request.method === "GET" && route.pathname === "/health") {
    reply(response, 200, { ok: true, issued: issued.size, repos });
    return;
  }

  if (request.method === "POST" && route.pathname === "/token") {
    let body;
    try {
      body = await readJson(request);
    } catch (error) {
      reply(response, 400, { error: error.message });
      return;
    }
    const { task, repo } = body;
    if (!task || !repo) {
      reply(response, 400, { error: "task and repo are required" });
      return;
    }
    if (!repos.includes(repo)) {
      console.error(`DENY task=${task} repo=${repo}`);
      reply(response, 403, { error: `${repo} is not in the broker allowlist` });
      return;
    }
    if (issued.has(task)) {
      reply(response, 409, { error: `${task} already holds a token; revoke it first` });
      return;
    }
    try {
      const minted = await mint(repo);
      issued.set(task, { token: minted.token, repo, expires_at: minted.expires_at });
      console.error(`MINT task=${task} repo=${repo} expires=${minted.expires_at}`);
      reply(response, 200, minted);
    } catch (error) {
      console.error(`FAIL task=${task} repo=${repo} ${error.message}`);
      reply(response, 502, { error: error.message });
    }
    return;
  }

  const revokeRoute = /^\/token\/(.+)$/.exec(route.pathname);
  if (request.method === "DELETE" && revokeRoute) {
    const task = decodeURIComponent(revokeRoute[1]);
    const held = issued.get(task);
    if (!held) {
      reply(response, 404, { error: `no token for ${task}` });
      return;
    }
    issued.delete(task);
    const revoked = await revoke(held.token);
    console.error(`REVOKE task=${task} repo=${held.repo} revoked=${revoked}`);
    reply(response, 204);
    return;
  }

  // The kill switch: revoke every token this broker remembers, not just the one
  // task that asked. Tokens are memory-only, so a broker restart already strands
  // them; this makes the same thing deliberate and immediate.
  if (request.method === "POST" && route.pathname === "/revoke-all") {
    const tasks = [...issued.keys()];
    let revoked = 0;
    for (const task of tasks) {
      const held = issued.get(task);
      issued.delete(task);
      if (await revoke(held.token)) revoked += 1;
      console.error(`REVOKE-ALL task=${task} repo=${held.repo}`);
    }
    reply(response, 200, { tasks: tasks.length, revoked });
    return;
  }

  reply(response, 404, { error: "not found" });
});

if (process.argv.includes("--selftest")) {
  // Prove the signing path without the network: sign, then verify with the public
  // half of the same key. A wrong key or a mangled base64url fails here.
  const [header, payload, signature] = appJwt().split(".");
  const publicKey = crypto.createPublicKey(privateKey);
  const verified = crypto
    .createVerify("RSA-SHA256")
    .update(`${header}.${payload}`)
    .verify(publicKey, Buffer.from(signature, "base64url"));
  const claims = JSON.parse(Buffer.from(payload, "base64url").toString());
  console.error(
    `selftest: verified=${verified} iss=${claims.iss} ttl=${claims.exp - claims.iat}s key=${keyFingerprint()}`,
  );
  process.exit(verified && claims.iss === appId ? 0 : 1);
}

server.listen(port, "127.0.0.1", () => {
  console.error(
    `crew-broker: listening on 127.0.0.1:${port} for ${repos.join(", ")} key=${keyFingerprint()}`,
  );
});
