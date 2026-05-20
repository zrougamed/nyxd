export function regexFromString(input) {
    const matches = input.match(/^\/(.*)\/(.*)|(.*)/);
    return matches && new RegExp(matches[1] || matches[3], matches[2]);
}
//# sourceMappingURL=regex-from-string.js.map