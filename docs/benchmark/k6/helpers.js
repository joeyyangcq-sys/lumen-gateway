import { check } from 'k6';
import { BASE_URL } from './config.js';

export function makePayload() {
    return JSON.stringify({
        market: 'BTC_USDT',
        base: 'BTC',
        quote: 'USDT',
        type: 'limit',
        price: '25000',
        size: '0.0001',
        side: 'sell',
        user_id: 1,
        text: 'benchmark-test',
    });
}

export function defaultHeaders() {
    return {
        'Connection': 'Keep-Alive',
        'Content-Type': 'application/json',
        'X-User-ID': '1',
    };
}

export function defaultParams() {
    return {
        headers: defaultHeaders(),
        timeout: '5s',
    };
}

export function buildUrl(path) {
    return `${BASE_URL}${path}`;
}

export function checkResponse(res, extraChecks) {
    const checks = {
        'status is 2xx': (r) => r.status >= 200 && r.status < 300,
        'body is not empty': (r) => r.body && r.body.length > 0,
    };

    if (extraChecks) {
        Object.assign(checks, extraChecks);
    }

    check(res, checks);
}
