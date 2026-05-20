import { validateMimeType } from '../../utils/validate-mime-type.js';
export const RequestMimeType = ({ allowedValues }) => {
    return {
        Root(root, ctx) {
            validateMimeType({ type: 'consumes', value: root }, ctx, allowedValues);
        },
        Operation: {
            leave(operation, ctx) {
                validateMimeType({ type: 'consumes', value: operation }, ctx, allowedValues);
            },
        },
    };
};
//# sourceMappingURL=request-mime-type.js.map