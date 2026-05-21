/**
 * Passthrough scenario — pure proxy, no plugins.
 *
 * Measures the raw gateway overhead with a constant 100 VU load over 60 s.
 * Both Lumen and APISIX are configured identically for this scenario:
 *   - Lumen: bench-echo route has no plugins; global access_log is absent.
 *   - APISIX: config-passthrough.yaml disables nginx access_log.
 *
 * Route: /benchmark/echo
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
    const res = http.post(buildUrl('/benchmark/echo'), makePayload(), defaultParams());
    checkResponse(res);
}
