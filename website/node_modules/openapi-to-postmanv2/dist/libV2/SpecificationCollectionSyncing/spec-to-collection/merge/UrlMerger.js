"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.mergeRequestUrlData = mergeRequestUrlData;
const QueryParamMerger_1 = require("./QueryParamMerger");
/**
 * Merges URL variables from target request into source request.
 * Preserves existing variable values and only adds new variables or removes variables not in target.
 * @param {Array} targetVariables - URL variables from the target request
 * @param {Array} sourceVariables - URL variables from the source request
 * @returns {Array} Merged variables array
 */
function mergeRequestUrlVariables(targetVariables, sourceVariables) {
    if (!targetVariables || targetVariables.length === 0) {
        return [];
    }
    if (!sourceVariables || sourceVariables.length === 0) {
        return targetVariables;
    }
    const sourceVariablesMap = new Map();
    sourceVariables.forEach((variable) => {
        if (variable?.key) {
            sourceVariablesMap.set(variable.key, variable);
        }
    });
    const mergedVariables = [];
    targetVariables.forEach((targetVariable) => {
        if (!targetVariable?.key) {
            return;
        }
        const existingVariable = sourceVariablesMap.get(targetVariable.key);
        if (existingVariable) {
            mergedVariables.push({
                ...targetVariable,
                value: existingVariable.value
            });
        }
        else {
            mergedVariables.push(targetVariable);
        }
    });
    return mergedVariables;
}
/**
 * Merges URL query and variable data
 * @param {string | UrlDefinition} targetUrl - Target URL
 * @param {string | UrlDefinition} sourceUrl - Source URL
 * @returns {string | UrlDefinition} Merged URL
 */
function mergeRequestUrlData(targetUrl, sourceUrl) {
    if (typeof targetUrl === 'string' || typeof sourceUrl === 'string') {
        return targetUrl;
    }
    const result = { ...targetUrl }, targetQuery = Array.isArray(targetUrl?.query) ? targetUrl.query : [], sourceQuery = Array.isArray(sourceUrl?.query) ? sourceUrl.query : [];
    result.query = (0, QueryParamMerger_1.mergeRequestQueryParams)(targetQuery, sourceQuery);
    const targetVariable = Array.isArray(targetUrl?.variable) ? targetUrl.variable : [], sourceVariable = Array.isArray(sourceUrl?.variable) ? sourceUrl.variable : [];
    result.variable = mergeRequestUrlVariables(targetVariable, sourceVariable);
    return result;
}
//# sourceMappingURL=UrlMerger.js.map