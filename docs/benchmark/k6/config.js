const GATEWAYS = {
    lumen: 'http://localhost:18080',
    apisix: 'http://localhost:9080',
};

export const BASE_URL = GATEWAYS[__ENV.GATEWAY] || GATEWAYS.lumen;
export const GATEWAY_NAME = __ENV.GATEWAY || 'lumen';

export const SUMMARY_TREND_STATS = ['avg', 'min', 'max', 'p(50)', 'p(75)', 'p(95)', 'p(99)', 'count'];

export const STANDARD_RATE = 2000;
export const STANDARD_DURATION = '30s';
export const STANDARD_VUS = 200;
