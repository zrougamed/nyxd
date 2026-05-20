export function getResolveConfig(resolve) {
    return {
        http: {
            headers: resolve?.http?.headers ?? [],
            customFetch: undefined,
        },
    };
}
//# sourceMappingURL=get-resolve-config.js.map