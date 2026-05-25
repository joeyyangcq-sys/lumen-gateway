/**
 * Spike scenario — resilience test on the pipeline route.
 *
 * Holds a steady 50 VU baseline, instantly spikes to 500 VUs, sustains the spike,
 * then drops back to baseline. Measures how quickly each gateway recovers and
 * whether errors spike during the transition.
 *
 * Route: /benchmark/pipeline
 */
import http from 'k6/http';
import { SUMMARY_TREND_STATS } from './config.js';
import { makePayload, defaultParams, buildUrl, checkResponse } from './helpers.js';

export const options = {
    scenarios: {
        spike: {
            executor: 'ramping-vus',
            startVUs: 50,
            stages: [
                { target: 50,  duration: '30s' }, // baseline
                { target: 500, duration: '10s' }, // instant spike
                { target: 500, duration: '30s' }, // sustain spike
                { target: 50,  duration: '10s' }, // instant drop
                { target: 50,  duration: '30s' }, // post-spike recovery
            ],
        },
    },
    thresholds: {
        http_req_failed: ['rate<0.05'],    // tolerate up to 5% errors during spike
        http_req_duration: ['p(99)<3000'], // P99 under 3 s during spike
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

export default function () {
    const res = http.post(buildUrl('/benchmark/pipeline'), makePayload(), defaultParams());
    checkResponse(res);
}
