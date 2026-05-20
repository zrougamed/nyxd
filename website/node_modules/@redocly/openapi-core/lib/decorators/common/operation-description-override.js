import { readFileAsStringSync, resolveRelativePath } from '../../utils/yaml-fs-helper.js';
export const OperationDescriptionOverride = ({ operationIds }) => {
    return {
        Operation: {
            leave(operation, { report, location, config }) {
                if (!operation.operationId)
                    return;
                if (!operationIds)
                    throw new Error(`Parameter "operationIds" is not provided for "operation-description-override" rule`);
                const operationId = operation.operationId;
                if (operationIds[operationId]) {
                    try {
                        const filePath = operationIds[operationId];
                        operation.description = readFileAsStringSync(resolveRelativePath(filePath, config?.configPath));
                    }
                    catch (e) {
                        report({
                            message: `Failed to read markdown override file for operation "${operationId}".\n${e.message}`,
                            location: location.child('operationId').key(),
                        });
                    }
                }
            },
        },
    };
};
//# sourceMappingURL=operation-description-override.js.map