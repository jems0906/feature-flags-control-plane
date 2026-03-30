// Minimal Node.js SDK for Feature Flags Control Plane
// Requires: npm install node-fetch eventsource

const fetch = require('node-fetch');
const EventSource = require('eventsource');

class FeatureFlagsClient {
  constructor(baseUrl, env = 'dev', authToken = '') {
    this.baseUrl = baseUrl;
    this.env = env;
    this.authToken = authToken;
    this.flags = [];
  }

  authHeaders() {
    return this.authToken ? { Authorization: `Bearer ${this.authToken}` } : {};
  }

  // -------------------------------------------------------------------------
  // Flag listing & hot-reload
  // -------------------------------------------------------------------------

  async getAllFlags() {
    const res = await fetch(`${this.baseUrl}/flags/all?env=${this.env}`);
    if (res.ok) {
      this.flags = await res.json();
      return this.flags;
    }
    return [];
  }

  pollFlags(interval = 10000) {
    setInterval(() => this.getAllFlags(), interval);
  }

  startSSE(onUpdate) {
    const url = `${this.baseUrl}/flags/stream?env=${this.env}`;
    const es = new EventSource(url);
    const applyUpdate = async (event) => {
      try {
        this.flags = JSON.parse(event.data);
      } catch (_) {
        this.flags = await this.getAllFlags();
      }
      if (onUpdate) onUpdate(this.flags);
    };

    es.onmessage = (event) => {
      if (event.type === 'update') {
        void applyUpdate(event);
      }
    };
    es.addEventListener('update', (event) => {
      void applyUpdate(event);
    });
    es.addEventListener('ping', () => {});
    return es;
  }

  // -------------------------------------------------------------------------
  // Feature flag evaluation
  // -------------------------------------------------------------------------

  /**
   * Evaluate a feature flag for a user/tenant context.
   * Returns a boolean; fails open (returns false) on network errors.
   */
  async isEnabled(flagName, userId = '', tenantId = '', headers = {}) {
    try {
      const res = await fetch(`${this.baseUrl}/flags/${flagName}/evaluate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ UserID: userId, TenantID: tenantId, Headers: headers }),
      });
      if (!res.ok) return false;
      const data = await res.json();
      return Boolean(data.enabled);
    } catch (_) {
      return false;
    }
  }

  // -------------------------------------------------------------------------
  // A/B experiments
  // -------------------------------------------------------------------------

  /**
   * Get the deterministic variant assigned to a user for an experiment.
   * Returns variant string, or empty string on error.
   */
  async getVariant(experimentName, userId) {
    try {
      const res = await fetch(
        `${this.baseUrl}/experiment/${experimentName}/variant?userId=${encodeURIComponent(userId)}`
      );
      if (!res.ok) return '';
      const data = await res.json();
      return data.variant || '';
    } catch (_) {
      return '';
    }
  }

  /**
   * Record a conversion event for an experiment variant.
   */
  async recordConversion(experimentName, variant) {
    try {
      await fetch(
        `${this.baseUrl}/experiment/${experimentName}/convert?variant=${encodeURIComponent(variant)}`,
        { method: 'POST', headers: this.authHeaders() }
      );
    } catch (_) {
      // non-critical; swallow
    }
  }

  // -------------------------------------------------------------------------
  // Rate limiting
  // -------------------------------------------------------------------------

  /**
   * Ask the control-plane whether the request is within rate limits.
   * Returns true (allowed) on network errors (fail open).
   */
  async checkRateLimit(route, userId = '', tenantId = '') {
    try {
      const res = await fetch(`${this.baseUrl}/ratelimit/check`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ route, userId, tenantId }),
      });
      if (!res.ok) return true;
      const data = await res.json();
      return Boolean(data.allowed);
    } catch (_) {
      return true; // fail open
    }
  }

  // -------------------------------------------------------------------------
  // Circuit breaking
  // -------------------------------------------------------------------------

  /**
   * Get the circuit breaker state for a route.
   * Returns one of: "closed", "open", "half-open".
   * Fails open (returns "closed") on errors.
   */
  async getCircuitBreakerState(route) {
    try {
      const res = await fetch(
        `${this.baseUrl}/circuitbreaker?route=${encodeURIComponent(route)}`
      );
      if (!res.ok) return 'closed';
      const data = await res.json();
      return data.state || 'closed';
    } catch (_) {
      return 'closed'; // fail open
    }
  }

  async checkCircuitBreaker(route) {
    try {
      const res = await fetch(`${this.baseUrl}/circuitbreaker/check`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ route }),
      });
      if (!res.ok) return { allowed: true, state: 'closed' };
      const data = await res.json();
      return { allowed: Boolean(data.allowed), state: data.state || 'closed' };
    } catch (_) {
      return { allowed: true, state: 'closed' };
    }
  }

  /**
   * Report a request outcome and latency to the control-plane so it can
   * update the circuit breaker state for the route.
   */
  async reportCircuitBreakerResult(route, success, latencyMs = 0) {
    try {
      await fetch(`${this.baseUrl}/circuitbreaker/report`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', ...this.authHeaders() },
        body: JSON.stringify({ route, success, latencyMs }),
      });
    } catch (_) {
      // non-critical; swallow
    }
  }
}

// Usage Example:
// const client = new FeatureFlagsClient('http://localhost:8080', 'dev');
// client.getAllFlags().then(console.log);
// client.startSSE(flags => console.log('Flags updated:', flags));
// client.isEnabled('dark-mode', 'alice').then(enabled => console.log('enabled:', enabled));
// client.getVariant('button-color', 'alice').then(v => console.log('variant:', v));
// client.checkRateLimit('/api/search', 'alice').then(ok => console.log('allowed:', ok));
// client.getCircuitBreakerState('/api/search').then(s => console.log('cb state:', s));

module.exports = FeatureFlagsClient;
