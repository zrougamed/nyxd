import { logger } from '../../logger.js';
const REQUIRED_VALUES_BY_AUTH_TYPE = {
    apiKey: ['apiKey'],
    basic: ['username', 'password'],
    digest: ['username', 'password'],
    bearer: ['token'],
    oauth2: ['accessToken'],
    openIdConnect: ['accessToken'],
    mutualTLS: [],
};
function validateSecuritySchemas(extendedSecurity, { report, location }) {
    if (!extendedSecurity) {
        return;
    }
    for (const securitySchema of extendedSecurity) {
        // TODO: Handle schemeName case, this will require bundled OpenAPI definitions
        if ('schemeName' in securitySchema) {
            continue;
        }
        const { scheme, values } = securitySchema;
        // TODO: Struct rule does not check before this point, so we need to check it here. Investigate if we can move this check to the Struct rule.
        const authType = scheme?.type === 'http' ? scheme.scheme : scheme?.type;
        if (authType === 'mutualTLS') {
            logger.warn('Please use CLI option to provide mutualTLS certificates for mutualTls authentication security schema.');
            continue;
        }
        const requiredValues = REQUIRED_VALUES_BY_AUTH_TYPE[authType];
        if (requiredValues) {
            for (const requiredValue of requiredValues) {
                if (!values || !(requiredValue in values)) {
                    report({
                        message: `The \`${requiredValue}\` is required when using the ${authType} authentication security schema.`,
                        location: location.child(['x-security', extendedSecurity.indexOf(securitySchema)]),
                    });
                }
            }
        }
        else {
            report({
                message: `The \`${authType}\` authentication security schema is not supported.`,
                location: location.child(['x-security', extendedSecurity.indexOf(securitySchema)]),
            });
        }
    }
}
export const XSecuritySchemaRequiredValues = () => {
    return {
        Step: {
            enter(step, context) {
                validateSecuritySchemas(step?.['x-security'], context);
            },
        },
        Workflow: {
            enter(workflow, context) {
                validateSecuritySchemas(workflow?.['x-security'], context);
            },
        },
    };
};
//# sourceMappingURL=x-security-scheme-required-values.js.map