import http from 'k6/http';
import { check } from 'k6';
import { Trend, Counter } from 'k6/metrics';
import { GATEWAY_URL, ADMIN_KEY, SUMMARY_TREND_STATS } from './config.js';

const createLatency = new Trend('admin_create_latency');
const deleteLatency = new Trend('admin_delete_latency');
const adminErrors = new Counter('admin_errors');

export const options = {
    scenarios: {
        admin_ops: {
            executor: 'constant-vus',
            vus: 20,
            duration: '30s',
        },
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

const ADMIN_HEADERS = {
    'Content-Type': 'application/json',
    'X-API-KEY': ADMIN_KEY,
};

export default function () {
    const id = `admin-bench-${__VU}-${__ITER}`;

    const createRes = http.put(
        `${GATEWAY_URL}/apisix/admin/upstreams/${id}`,
        JSON.stringify({ type: 'roundrobin', scheme: 'http', nodes: { 'localhost:9001': 1 } }),
        { headers: ADMIN_HEADERS, timeout: '10s' }
    );

    createLatency.add(createRes.timings.duration);
    if (createRes.status !== 200 && createRes.status !== 201) {
        adminErrors.add(1);
    }

    check(createRes, { 'create 2xx': (r) => r.status >= 200 && r.status < 300 });

    const deleteRes = http.del(
        `${GATEWAY_URL}/apisix/admin/upstreams/${id}`,
        null,
        { headers: ADMIN_HEADERS, timeout: '10s' }
    );

    deleteLatency.add(deleteRes.timings.duration);
    check(deleteRes, { 'delete 2xx': (r) => r.status >= 200 && r.status < 300 });
}
