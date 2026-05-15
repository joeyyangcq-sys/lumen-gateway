import http from 'k6/http';
import { check, group } from 'k6';
import { Trend } from 'k6/metrics';
import { GATEWAY_URL, ROUTE_COUNT, SUMMARY_TREND_STATS } from './config.js';

const q1Latency = new Trend('latency_q1_first_quarter');
const q2Latency = new Trend('latency_q2_second_quarter');
const q3Latency = new Trend('latency_q3_third_quarter');
const q4Latency = new Trend('latency_q4_last_quarter');

export const options = {
    scenarios: {
        spread: {
            executor: 'constant-vus',
            vus: 100,
            duration: '30s',
        },
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

const PAYLOAD = JSON.stringify({ test: true });
const PARAMS = {
    headers: { 'Content-Type': 'application/json', 'Connection': 'Keep-Alive' },
    timeout: '5s',
};

export default function () {
    const idx = Math.floor(Math.random() * ROUTE_COUNT);
    const res = http.post(`${GATEWAY_URL}/stress/${idx}`, PAYLOAD, PARAMS);

    check(res, { 'status 200': (r) => r.status === 200 });

    const quarter = Math.floor(idx / (ROUTE_COUNT / 4));
    const dur = res.timings.duration;
    if (quarter === 0) q1Latency.add(dur);
    else if (quarter === 1) q2Latency.add(dur);
    else if (quarter === 2) q3Latency.add(dur);
    else q4Latency.add(dur);
}
