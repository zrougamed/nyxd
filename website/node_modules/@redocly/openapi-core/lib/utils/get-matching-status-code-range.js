export function getMatchingStatusCodeRange(code) {
    return `${code}`.replace(/^(\d)\d\d$/, (_, firstDigit) => `${firstDigit}XX`);
}
//# sourceMappingURL=get-matching-status-code-range.js.map