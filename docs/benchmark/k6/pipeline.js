/**
 * Pipeline scenario — comparable plugin stacks on both gateways.
 *
 * Measures overhead when both gateways run equivalent plugin work:
 *   - Lumen: bench-pipeline route runs request_id + access_log (16 KB buffer, 1 s flush).
 *   - APISIX: config-pipeline.yaml enables buffered nginx access_log (16 KB / 1 s);
 *             bench-pipeline route has the request-id plugin configured via Admin API.
 *
 * Constant 100 VUs for 60 s — same load shape as passthrough for direct comparison.
 *
 * Route: /benchmark/pipeline
 */
import http from 'k6/http';
import { SUMMARY_TREND_STATS, COMMON_THRESHOLDS } from './config.js';
import { makePayload, defaultParams, buildUrl, checkResponse } from './helpers.js';

export const options = {
    scenarios: {
        constant_load: {
            executor: 'constant-vus',
            vus: 300,
            duration: '60s',
        },
    },
    thresholds: COMMON_THRESHOLDS,
    summaryTrendStats: SUMMARY_TREND_STATS,
};

export default function () {
    const res = http.post(buildUrl('/benchmark/pipeline'), makePayload(), defaultParams());
    checkResponse(res);
}
