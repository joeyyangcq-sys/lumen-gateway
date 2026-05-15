import http from 'k6/http';
import { check } from 'k6';
import { GATEWAY_URL, ROUTE_COUNT, SUMMARY_TREND_STATS } from './config.js';

export const options = {
    scenarios: {
        constant_load: {
            executor: 'constant-vus',
            vus: 100,
            duration: '30s',
        },
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

const PAYLOAD = JSON.stringify({ market: 'BTC_USDT', type: 'limit', price: '25000', size: '0.0001' });
const PARAMS = {
    headers: { 'Content-Type': 'application/json', 'Connection': 'Keep-Alive' },
    timeout: '5s',
};

export default function () {
    const idx = Math.floor(Math.random() * ROUTE_COUNT);
    const res = http.post(`${GATEWAY_URL}/stress/${idx}`, PAYLOAD, PARAMS);
    check(res, {
        'status 200': (r) => r.status === 200,
        'body not empty': (r) => r.body && r.body.length > 0,
    });
}
