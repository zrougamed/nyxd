import { validateDefinedAndNonEmpty } from '../utils.js';
export const OperationSummary = () => {
    return {
        Operation(operation, ctx) {
            validateDefinedAndNonEmpty('summary', operation, ctx);
        },
    };
};
//# sourceMappingURL=operation-summary.js.map