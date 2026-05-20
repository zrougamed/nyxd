import { getMatchingStatusCodeRange } from '../../utils/get-matching-status-code-range.js';
export const ResponseContainsProperty = (options) => {
    const names = options.names || {};
    let key;
    return {
        Operation: {
            Response: {
                skip: (_response, key) => {
                    return `${key}` === '204';
                },
                enter: (_response, ctx) => {
                    key = ctx.key;
                },
                Schema(schema, { report, location }) {
                    if (schema.type !== 'object')
                        return;
                    const expectedProperties = names[key] ||
                        names[getMatchingStatusCodeRange(key)] ||
                        names[getMatchingStatusCodeRange(key).toLowerCase()] ||
                        [];
                    for (const expectedProperty of expectedProperties) {
                        if (!schema.properties?.[expectedProperty]) {
                            report({
                                message: `Response object must contain a top-level "${expectedProperty}" property.`,
                                location: location.child('properties').key(),
                            });
                        }
                    }
                },
            },
        },
    };
};
//# sourceMappingURL=response-contains-property.js.map