export const GATEWAY_URL = __ENV.GATEWAY_URL || 'http://localhost:18080';
export const ADMIN_KEY = __ENV.ADMIN_KEY || 'local-dev-admin-key';
export const ROUTE_COUNT = parseInt(__ENV.ROUTE_COUNT || '1000', 10);

export const SUMMARY_TREND_STATS = ['avg', 'min', 'max', 'p(50)', 'p(75)', 'p(90)', 'p(95)', 'p(99)', 'count'];
