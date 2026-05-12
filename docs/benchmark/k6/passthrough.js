import http from 'k6/http';
import { SUMMARY_TREND_STATS } from './config.js';
import { makePayload, defaultParams, buildUrl, checkResponse } from './helpers.js';

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

export default function () {
    const res = http.post(buildUrl('/benchmark/echo'), makePayload(), defaultParams());
    checkResponse(res);
}
