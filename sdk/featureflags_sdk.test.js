const assert = require('node:assert/strict');
const Module = require('node:module');

let fetchImpl = async () => ({ ok: true, json: async () => ({}) });

const originalLoad = Module._load;
Module._load = function patchedLoad(request, parent, isMain) {
  if (request === 'node-fetch') {
    return (...args) => fetchImpl(...args);
  }
  if (request === 'eventsource') {
    return class FakeEventSource {};
  }
  return originalLoad(request, parent, isMain);
};

const FeatureFlagsClient = require('./featureflags_sdk');
Module._load = originalLoad;

async function testRecordConversionUsesBearerToken() {
  const calls = [];
  fetchImpl = async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({}) };
  };

  const client = new FeatureFlagsClient('http://localhost:8080', 'dev', 'secret-token');
  await client.recordConversion('button-color', 'green');

  assert.equal(calls.length, 1);
  assert.equal(
    calls[0].url,
    'http://localhost:8080/experiment/button-color/convert?variant=green'
  );
  assert.equal(calls[0].options.method, 'POST');
  assert.equal(calls[0].options.headers.Authorization, 'Bearer secret-token');
}

async function testReportCircuitBreakerUsesBearerToken() {
  const calls = [];
  fetchImpl = async (url, options = {}) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({}) };
  };

  const client = new FeatureFlagsClient('http://localhost:8080', 'dev', 'secret-token');
  await client.reportCircuitBreakerResult('/demo/action', false, 320);

  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'http://localhost:8080/circuitbreaker/report');
  assert.equal(calls[0].options.method, 'POST');
  assert.equal(calls[0].options.headers.Authorization, 'Bearer secret-token');
  assert.equal(
    calls[0].options.body,
    JSON.stringify({ route: '/demo/action', success: false, latencyMs: 320 })
  );
}

async function testCheckCircuitBreakerParsesResponse() {
  fetchImpl = async () => ({
    ok: true,
    json: async () => ({ allowed: false, state: 'half-open' }),
  });

  const client = new FeatureFlagsClient('http://localhost:8080');
  const result = await client.checkCircuitBreaker('/demo/action');

  assert.deepEqual(result, { allowed: false, state: 'half-open' });
}

async function main() {
  await testRecordConversionUsesBearerToken();
  await testReportCircuitBreakerUsesBearerToken();
  await testCheckCircuitBreakerParsesResponse();
  console.log('featureflags_sdk.test.js: PASS');
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});