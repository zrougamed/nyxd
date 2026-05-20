import { validateResponseCodes } from '../utils.js';
export const Operation2xxResponse = ({ validateWebhooks }) => {
    return {
        Paths: {
            Responses(responses, { report }) {
                const codes = Object.keys(responses || {});
                validateResponseCodes(codes, '2XX', { report });
            },
        },
        WebhooksMap: {
            Responses(responses, { report }) {
                if (!validateWebhooks)
                    return;
                const codes = Object.keys(responses || {});
                validateResponseCodes(codes, '2XX', { report });
            },
        },
    };
};
//# sourceMappingURL=operation-2xx-response.js.map