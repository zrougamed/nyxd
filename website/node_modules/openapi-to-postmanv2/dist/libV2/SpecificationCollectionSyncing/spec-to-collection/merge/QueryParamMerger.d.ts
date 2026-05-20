import { QueryParamDefinition } from 'postman-collection';
/**
 * Merges query parameters from target request into source request.
 * Preserves existing query parameter values and only adds new params or removes params not in latest.
 * @param {QueryParamDefinition[]} targetQuery - Query params from the target request
 * @param {QueryParamDefinition[]} sourceQuery - Query params from the current request
 * @returns {QueryParamDefinition[]} Merged query params array
 */
export declare function mergeRequestQueryParams(targetQuery: QueryParamDefinition[], sourceQuery: QueryParamDefinition[]): QueryParamDefinition[];
//# sourceMappingURL=QueryParamMerger.d.ts.map