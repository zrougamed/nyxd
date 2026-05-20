export const PathNotIncludeQuery = () => {
    return {
        Paths: {
            PathItem(_operation, { report, key }) {
                if (key.toString().includes('?')) {
                    report({
                        message: `Don't put query string items in the path, they belong in parameters with \`in: query\`.`,
                        location: { reportOnKey: true },
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=path-not-include-query.js.map