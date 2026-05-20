import { validateDefinedAndNonEmpty } from '../utils.js';
export const OperationOperationId = () => {
    return {
        Root: {
            PathItem: {
                Operation(operation, ctx) {
                    validateDefinedAndNonEmpty('operationId', operation, ctx);
                },
            },
        },
    };
};
//# sourceMappingURL=operation-operationId.js.map