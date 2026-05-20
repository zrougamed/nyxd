import { getOwn } from '../../utils/get-own.js';
export const NoXSecurityBothSchemeAndSchemeName = () => {
    function validate(extendedSecurity, { report, location }) {
        if (!Array.isArray(extendedSecurity))
            return;
        for (const security of extendedSecurity) {
            const hasScheme = getOwn(security, 'scheme');
            const hasSchemeName = getOwn(security, 'schemeName');
            if (hasScheme && hasSchemeName) {
                report({
                    message: '`x-security` item must not contain both `scheme` and `schemeName`.',
                    location: location.child(['x-security', extendedSecurity.indexOf(security)]),
                });
            }
        }
    }
    return {
        Workflow: {
            enter(workflow, context) {
                validate(workflow?.['x-security'], context);
            },
        },
        Step: {
            enter(step, context) {
                validate(step?.['x-security'], context);
            },
        },
    };
};
//# sourceMappingURL=no-x-security-both-scheme-and-scheme-name.js.map