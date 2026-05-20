export const NoXSecuritySchemeNameWithoutOpenAPI = () => {
    return {
        Step: {
            enter(step, { report, location }) {
                const hasExtendedOperation = step?.['x-operation'];
                if (!hasExtendedOperation) {
                    return;
                }
                const extendedSecurity = step?.['x-security'];
                if (!extendedSecurity) {
                    return;
                }
                for (const security of extendedSecurity) {
                    if ('schemeName' in security) {
                        report({
                            message: 'The `schemeName` can be only used in Step with OpenAPI operation, please use `scheme` instead.',
                            location: location.child(['x-security', extendedSecurity.indexOf(security)]),
                        });
                    }
                }
            },
        },
    };
};
//# sourceMappingURL=no-x-security-scheme-name-without-openapi.js.map