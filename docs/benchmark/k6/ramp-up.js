import http from 'k6/http';
import { SUMMARY_TREND_STATS } from './config.js';
import { makePayload, defaultParams, buildUrl, checkResponse } from './helpers.js';

export const options = {
    scenarios: {
        ramp: {
            executor: 'ramping-vus',
            startVUs: 10,
            stages: [
                { target: 50, duration: '10s' },
                { target: 100, duration: '10s' },
                { target: 200, duration: '10s' },
                { target: 300, duration: '10s' },
                { target: 500, duration: '10s' },
            ],
        },
    },
    summaryTrendStats: SUMMARY_TREND_STATS,
};

export default function () {
    const res = http.post(buildUrl('/benchmark/echo'), makePayload(), defaultParams());
    checkResponse(res);
}
