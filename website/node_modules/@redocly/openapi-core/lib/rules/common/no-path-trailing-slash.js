export const NoPathTrailingSlash = () => {
    return {
        PathItem(_path, { report, key, rawLocation }) {
            if (key.endsWith('/') && key !== '/') {
                report({
                    message: `\`${key}\` should not have a trailing slash.`,
                    location: rawLocation.key(),
                });
            }
        },
    };
};
//# sourceMappingURL=no-path-trailing-slash.js.map