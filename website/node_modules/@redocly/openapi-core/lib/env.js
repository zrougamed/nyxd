export const isBrowser = typeof window !== 'undefined' ||
    typeof process === 'undefined' ||
    process?.platform === 'browser'; // main and worker thread
export const env = isBrowser ? {} : process.env || {};
//# sourceMappingURL=env.js.map