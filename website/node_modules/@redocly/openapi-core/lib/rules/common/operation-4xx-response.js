import { validateResponseCodes } from '../utils.js';
export const Operation4xxResponse = ({ validateWebhooks }) => {
    return {
        Paths: {
            Responses(responses, { report }) {
                const codes = Object.keys(responses || {});
                validateResponseCodes(codes, '4XX', { report });
            },
        },
        WebhooksMap: {
            Responses(responses, { report }) {
                if (!validateWebhooks)
                    return;
                const codes = Object.keys(responses || {});
                validateResponseCodes(codes, '4XX', { report });
            },
        },
    };
};
//# sourceMappingURL=operation-4xx-response.js.map