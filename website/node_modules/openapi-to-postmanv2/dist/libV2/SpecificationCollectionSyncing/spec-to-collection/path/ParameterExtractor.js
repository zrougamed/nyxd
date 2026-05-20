"use strict";
/**
 * Path Parameter Extraction and Mapping Utilities
 * Functions for extracting Postman variables from paths and mapping specification path parameters
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.extractPostmanVariablesFromPathComponents = extractPostmanVariablesFromPathComponents;
const postman_collection_1 = require("postman-collection");
/**
 * Extracts variables to add when {{xyz}} components in current path match :xyz parameters in latest path.
 * When a {{xyz}} component in current path matches a :xyz component in latest path,
 * the {{xyz}} variable is returned for addition to the URL variables.
 *
 * @param {string[]} latestPath - The latest path components to sync from (may contain :xyz parameters)
 * @param {string[]} currentPath - The current path components (may contain {{xyz}} variables)
 * @returns {Array<Variable>} Array of variables to add to the URL
 */
function extractPostmanVariablesFromPathComponents(latestPath, currentPath) {
    if (!latestPath || !currentPath || latestPath.length !== currentPath.length) {
        return [];
    }
    const variablesToAdd = [];
    for (let i = 0; i < latestPath.length; i++) {
        const latestComponent = latestPath[i], currentComponent = currentPath[i], isCurrentVariable = currentComponent.startsWith('{{') && currentComponent.endsWith('}}') && currentComponent.length > 4, isLatestParameter = latestComponent.startsWith(':') && latestComponent.length > 1;
        if (isCurrentVariable && isLatestParameter) {
            // Extract parameter name from :xyz format
            const parameterName = latestComponent.slice(1);
            variablesToAdd.push(new postman_collection_1.Variable({
                key: parameterName,
                value: currentComponent
            }));
        }
    }
    return variablesToAdd;
}
//# sourceMappingURL=ParameterExtractor.js.map