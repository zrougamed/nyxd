// eslint-disable-next-line no-useless-escape
const validUrlSymbols = /^[A-Za-z0-9-._~:/?#\[\]@!\$&'()*+,;=]*$/;
export const OperationIdUrlSafe = () => {
    return {
        Operation(operation, { report, location }) {
            if (operation.operationId && !validUrlSymbols.test(operation.operationId)) {
                report({
                    message: 'Operation `operationId` should not have URL invalid characters.',
                    location: location.child(['operationId']),
                });
            }
        },
    };
};
//# sourceMappingURL=operation-operationId-url-safe.js.map