/**
 * Ramp-up scenario — gradual saturation test on the pipeline route.
 *
 * Ramps from 0 to 500 VUs in 3 stages, sustains peak load, then ramps back down.
 * Identifies where each gateway starts degrading and at what concurrency it saturates.
 *
 * Uses the pipeline route (/benchmark/pipeline) with both gateways running their
 * full plugin stacks for a fair apples-to-apples saturation comparison.
 *
 * Route: /benchmark/pipeline
 */
import http from 'k6/http';
import { SUMMARY_TREND_STATS } from './config.js';
import { makePayload, defaultParams, buildUrl, checkResponse } from './helpers.js';

export const options = {
    scenarios: {
        ramp: {
            executor: 'ramping-vus',
            startVUs: 0,
            stages: [
                { target: 100, duration: '30s' }, // warm ramp
                { target: 300, duration: '30s' }, // moderate load
                { target: 500, duration: '30s' }, // peak load
                { target: 500, duration: '30s' }, // sustain peak
                { target: 0,   duration: '30s' }, // ramp down
            ],
        },
    },
    thresholds: {
        http_req_failed: ['rate<0.01'],    // < 1% errors even at peak
        http_req_duration: ['p(99)<2000'], // P99 under 2 s at 500 VUs
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

export default function () {
    const res = http.post(buildUrl('/benchmark/pipeline'), makePayload(), defaultParams());
    checkResponse(res);
}
