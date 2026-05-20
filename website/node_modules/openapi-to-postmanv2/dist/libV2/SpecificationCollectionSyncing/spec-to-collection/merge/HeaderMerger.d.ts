import { HeaderDefinition } from 'postman-collection';
/**
 * Merges headers from target request into source request.
 * Preserves existing header values and only adds new headers or removes headers not in latest.
 * @param {HeaderDefinition[] | undefined} targetHeaders - Headers from the target request
 * @param {HeaderDefinition[] | undefined} sourceHeaders - Headers from the source request
 * @returns {HeaderDefinition[]} Merged headers array
 */
export declare function mergeRequestAndResponseHeaders(targetHeaders: HeaderDefinition[] | undefined, sourceHeaders: HeaderDefinition[] | undefined): HeaderDefinition[];
//# sourceMappingURL=HeaderMerger.d.ts.map