import { validateMimeTypeOAS3 } from '../../utils/validate-mime-type.js';
export const RequestMimeType = ({ allowedValues }) => {
    return {
        Paths: {
            RequestBody: {
                leave(requestBody, ctx) {
                    validateMimeTypeOAS3({ type: 'consumes', value: requestBody }, ctx, allowedValues);
                },
            },
            Callback: {
                RequestBody() { },
                Response: {
                    leave(response, ctx) {
                        validateMimeTypeOAS3({ type: 'consumes', value: response }, ctx, allowedValues);
                    },
                },
            },
        },
        WebhooksMap: {
            Response: {
                leave(response, ctx) {
                    validateMimeTypeOAS3({ type: 'consumes', value: response }, ctx, allowedValues);
                },
            },
        },
    };
};
//# sourceMappingURL=request-mime-type.js.map