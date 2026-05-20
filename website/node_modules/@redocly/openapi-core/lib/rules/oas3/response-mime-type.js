import { validateMimeTypeOAS3 } from '../../utils/validate-mime-type.js';
export const ResponseMimeType = ({ allowedValues }) => {
    return {
        Paths: {
            Response: {
                leave(response, ctx) {
                    validateMimeTypeOAS3({ type: 'produces', value: response }, ctx, allowedValues);
                },
            },
            Callback: {
                Response() { },
                RequestBody: {
                    leave(requestBody, ctx) {
                        validateMimeTypeOAS3({ type: 'produces', value: requestBody }, ctx, allowedValues);
                    },
                },
            },
        },
        WebhooksMap: {
            RequestBody: {
                leave(requestBody, ctx) {
                    validateMimeTypeOAS3({ type: 'produces', value: requestBody }, ctx, allowedValues);
                },
            },
        },
    };
};
//# sourceMappingURL=response-mime-type.js.map