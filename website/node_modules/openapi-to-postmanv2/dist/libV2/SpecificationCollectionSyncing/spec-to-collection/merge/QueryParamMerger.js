"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.mergeRequestQueryParams = mergeRequestQueryParams;
const lodash_1 = __importDefault(require("lodash"));
/**
 * Merges query parameters from target request into source request.
 * Preserves existing query parameter values and only adds new params or removes params not in latest.
 * @param {QueryParamDefinition[]} targetQuery - Query params from the target request
 * @param {QueryParamDefinition[]} sourceQuery - Query params from the current request
 * @returns {QueryParamDefinition[]} Merged query params array
 */
function mergeRequestQueryParams(targetQuery, sourceQuery) {
    if (!targetQuery || targetQuery.length === 0) {
        return [];
    }
    if (!sourceQuery || sourceQuery.length === 0) {
        return targetQuery;
    }
    const sourceQueryMap = new Map();
    sourceQuery.forEach((param) => {
        if (param?.key) {
            sourceQueryMap.set(param.key, param);
        }
    });
    const mergedQuery = [];
    targetQuery.forEach((targetParam) => {
        if (!targetParam || lodash_1.default.isNil(targetParam.key)) {
            return;
        }
        const existingParam = sourceQueryMap.get(targetParam.key);
        if (existingParam) {
            mergedQuery.push({
                ...targetParam,
                value: existingParam.value,
                disabled: existingParam.disabled
            });
        }
        else {
            mergedQuery.push(targetParam);
        }
    });
    return mergedQuery;
}
//# sourceMappingURL=QueryParamMerger.js.map