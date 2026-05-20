import { validateDefinedAndNonEmpty } from '../utils.js';
/**
 * Validation according to rfc7807 - https://datatracker.ietf.org/doc/html/rfc7807
 */
export const Operation4xxProblemDetailsRfc7807 = () => {
    return {
        Response: {
            skip(_, key) {
                return !/4[Xx0-9]{2}/.test(`${key}`);
            },
            enter(response, { report, location }) {
                if (!response.content || !response.content['application/problem+json'])
                    report({
                        message: 'Response `4xx` must have content-type `application/problem+json`.',
                        location: location.key(),
                    });
            },
            MediaType: {
                skip(_, key) {
                    return key !== 'application/problem+json';
                },
                enter(media, ctx) {
                    validateDefinedAndNonEmpty('schema', media, ctx);
                },
                SchemaProperties(schema, ctx) {
                    validateDefinedAndNonEmpty('type', schema, ctx);
                    validateDefinedAndNonEmpty('title', schema, ctx);
                },
            },
        },
    };
};
//# sourceMappingURL=operation-4xx-problem-details-rfc7807.js.map