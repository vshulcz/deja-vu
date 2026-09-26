import assert from "node:assert/strict";
import test from "node:test";
import { preflightClawhubPublish } from "./release-clawhub-auth.mjs";

const requestToken = "test-request-token";
const githubOidcToken = "test-github-oidc-token";
const env = {
  ACTIONS_ID_TOKEN_REQUEST_URL: "https://token.actions.githubusercontent.com/oidc",
  ACTIONS_ID_TOKEN_REQUEST_TOKEN: requestToken,
};

test("missing Actions OIDC environment fails before fetch", async () => {
  let called = false;
  await assert.rejects(
    preflightClawhubPublish("@scope/pkg", "1.0.0", {
      env: {
        ACTIONS_ID_TOKEN_REQUEST_URL: env.ACTIONS_ID_TOKEN_REQUEST_URL,
      },
      fetchImpl: async () => {
        called = true;
      },
    }),
    /before GitHub OIDC/,
  );
  assert.equal(called, false);
});

test("GitHub OIDC request failures name that phase", async () => {
  let calls = 0;
  await assert.rejects(
    preflightClawhubPublish("@scope/pkg", "1.0.0", {
      env,
      fetchImpl: async () => {
        calls += 1;
        return new Response("oidc denied", { status: 403 });
      },
    }),
    /during GitHub OIDC token request \(HTTP 403: oidc denied\)/,
  );
  assert.equal(calls, 1);
});

test("ClawHub mint failures name that phase and redact the OIDC token", async () => {
  const fetchImpl = async (input, init) => {
    const url = String(input);
    if (url.startsWith(env.ACTIONS_ID_TOKEN_REQUEST_URL)) {
      assert.equal(new URL(url).searchParams.get("audience"), "clawhub");
      assert.equal(init.headers.Authorization, `Bearer ${requestToken}`);
      return new Response(JSON.stringify({ value: githubOidcToken }), { status: 200 });
    }
    assert.equal(url, "https://clawhub.ai/api/v1/publish/token/mint");
    assert.deepEqual(JSON.parse(init.body), {
      packageName: "@scope/pkg",
      version: "1.0.0",
      githubOidcToken,
    });
    return new Response(`refused ${githubOidcToken}`, { status: 403 });
  };

  let failure;
  try {
    await preflightClawhubPublish("@scope/pkg", "1.0.0", { env, fetchImpl });
  } catch (error) {
    failure = error;
  }
  assert.match(
    failure?.message ?? "",
    /during ClawHub publish-token mint \(HTTP 403: refused \[redacted\]\)/,
  );
  assert.doesNotMatch(failure?.message ?? "", new RegExp(githubOidcToken));
});

test("successful preflight returns metadata without a publish token", async () => {
  const fetchImpl = async (input) => {
    const url = String(input);
    if (url.startsWith(env.ACTIONS_ID_TOKEN_REQUEST_URL)) {
      return new Response(JSON.stringify({ value: githubOidcToken }), { status: 200 });
    }
    return new Response(JSON.stringify({ token: "test-publish-token", expiresAt: 123 }), {
      status: 200,
    });
  };

  const result = await preflightClawhubPublish("@scope/pkg", "1.0.0", { env, fetchImpl });
  assert.deepEqual(result, { expiresAt: 123 });
  assert.equal("token" in result, false);
});
