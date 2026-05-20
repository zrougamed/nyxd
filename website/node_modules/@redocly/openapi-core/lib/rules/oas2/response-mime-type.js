import { validateMimeType } from '../../utils/validate-mime-type.js';
export const ResponseMimeType = ({ allowedValues }) => {
    return {
        Root(root, ctx) {
            validateMimeType({ type: 'produces', value: root }, ctx, allowedValues);
        },
        Operation: {
            leave(operation, ctx) {
                validateMimeType({ type: 'produces', value: operation }, ctx, allowedValues);
            },
        },
    };
};
//# sourceMappingURL=response-mime-type.js.map