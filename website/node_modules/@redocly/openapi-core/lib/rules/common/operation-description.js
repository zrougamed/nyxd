import { validateDefinedAndNonEmpty } from '../utils.js';
export const OperationDescription = () => {
    return {
        Operation(operation, ctx) {
            validateDefinedAndNonEmpty('description', operation, ctx);
        },
    };
};
//# sourceMappingURL=operation-description.js.map