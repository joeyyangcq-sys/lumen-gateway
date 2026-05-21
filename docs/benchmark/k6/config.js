const GATEWAYS = {
    lumen: 'http://localhost:18080',
    apisix: 'http://localhost:9080',
};

export const BASE_URL = GATEWAYS[__ENV.GATEWAY] || GATEWAYS.lumen;
export const GATEWAY_NAME = __ENV.GATEWAY || 'lumen';

export const SUMMARY_TREND_STATS = ['avg', 'min', 'max', 'p(50)', 'p(75)', 'p(90)', 'p(95)', 'p(99)', 'count'];

// Standard thresholds for constant-load scenarios (100 VUs / 60s).
// Spike and ramp-up scripts override these with looser bounds.
export const COMMON_THRESHOLDS = {
    http_req_failed: ['rate<0.01'],    // < 1% errors
    http_req_duration: ['p(99)<1000'], // P99 under 1000 ms at 300 VUs
};
