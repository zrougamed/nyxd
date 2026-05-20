export const OperationIdUnique = () => {
    const seenOperations = new Set();
    return {
        Operation(operation, { report, location }) {
            if (!operation.operationId)
                return;
            if (seenOperations.has(operation.operationId)) {
                report({
                    message: 'Every operation must have a unique `operationId`.',
                    location: location.child([operation.operationId]),
                });
            }
            seenOperations.add(operation.operationId);
        },
    };
};
//# sourceMappingURL=operation-operationId-unique.js.map