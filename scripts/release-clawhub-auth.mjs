import { pathToFileURL } from "node:url";

const audience = "clawhub";
const defaultRegistry = "https://clawhub.ai";

function redact(value, secrets) {
  let text = String(value ?? "").trim();
  for (const secret of secrets) {
    if (secret) text = text.split(secret).join("[redacted]");
  }
  if (text.length > 512) text = `${text.slice(0, 512)}...`;
  return text;
}

function responseError(response, body, secrets) {
  const detail = redact(body || response.statusText, secrets);
  return detail
    ? ` (HTTP ${response.status}: ${detail})`
    : ` (HTTP ${response.status})`;
}

export async function preflightClawhubPublish(packageName, version, {
  env = process.env,
  fetchImpl = globalThis.fetch,
  registry = env.CLAWHUB_REGISTRY || defaultRegistry,
} = {}) {
  if (!packageName?.trim() || !version?.trim()) {
    throw new Error("ClawHub package name and version are required");
  }

  const requestUrl = env.ACTIONS_ID_TOKEN_REQUEST_URL?.trim();
  const requestToken = env.ACTIONS_ID_TOKEN_REQUEST_TOKEN?.trim();
  if (!requestUrl || !requestToken) {
    throw new Error(
      "ClawHub trusted-publishing preflight failed before GitHub OIDC: both ACTIONS_ID_TOKEN_REQUEST_URL and ACTIONS_ID_TOKEN_REQUEST_TOKEN are required",
    );
  }

  let oidcUrl;
  try {
    oidcUrl = new URL(requestUrl);
    oidcUrl.searchParams.set("audience", audience);
  } catch (error) {
    throw new Error(
      `ClawHub trusted-publishing preflight failed before GitHub OIDC: ${redact(error instanceof Error ? error.message : error, [requestToken])}`,
    );
  }

  let githubResponse;
  let githubBody;
  try {
    githubResponse = await fetchImpl(oidcUrl, {
      headers: {
        Accept: "application/json",
        Authorization: `Bearer ${requestToken}`,
      },
    });
    githubBody = await githubResponse.text();
  } catch (error) {
    throw new Error(
      `ClawHub trusted-publishing preflight failed during GitHub OIDC token request: ${redact(error instanceof Error ? error.message : error, [requestToken])}`,
    );
  }
  if (!githubResponse.ok) {
    throw new Error(
      `ClawHub trusted-publishing preflight failed during GitHub OIDC token request${responseError(githubResponse, githubBody, [requestToken])}`,
    );
  }

  let githubPayload;
  try {
    githubPayload = JSON.parse(githubBody);
  } catch {
    throw new Error(
      "ClawHub trusted-publishing preflight failed during GitHub OIDC token request: response was not JSON",
    );
  }
  const githubOidcToken =
    typeof githubPayload?.value === "string" ? githubPayload.value.trim() : "";
  if (!githubOidcToken) {
    throw new Error(
      "ClawHub trusted-publishing preflight failed during GitHub OIDC token request: response did not include a token",
    );
  }

  const mintUrl = new URL("/api/v1/publish/token/mint", registry);
  let mintResponse;
  let mintBody;
  try {
    mintResponse = await fetchImpl(mintUrl, {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        packageName: packageName.trim(),
        version: version.trim(),
        githubOidcToken,
      }),
    });
    mintBody = await mintResponse.text();
  } catch (error) {
    throw new Error(
      `ClawHub trusted-publishing preflight failed during ClawHub publish-token mint: ${redact(error instanceof Error ? error.message : error, [requestToken, githubOidcToken])}`,
    );
  }
  if (!mintResponse.ok) {
    throw new Error(
      `ClawHub trusted-publishing preflight failed during ClawHub publish-token mint${responseError(mintResponse, mintBody, [requestToken, githubOidcToken])}`,
    );
  }

  let mintPayload;
  try {
    mintPayload = JSON.parse(mintBody);
  } catch {
    throw new Error(
      "ClawHub trusted-publishing preflight failed during ClawHub publish-token mint: response was not JSON",
    );
  }
  if (typeof mintPayload?.token !== "string" || !mintPayload.token.trim()) {
    throw new Error(
      "ClawHub trusted-publishing preflight failed during ClawHub publish-token mint: response did not include a token",
    );
  }

  return {
    expiresAt: typeof mintPayload.expiresAt === "number" ? mintPayload.expiresAt : null,
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [packageName, version] = process.argv.slice(2);
  preflightClawhubPublish(packageName, version)
    .then(() => {
      console.log("ClawHub trusted-publishing preflight passed.");
    })
    .catch((error) => {
      console.error(error instanceof Error ? error.message : String(error));
      process.exitCode = 1;
    });
}
