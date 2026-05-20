import { RequestBodyDefinition } from 'postman-collection';
/**
 * Merges body.raw from target request into source request.
 * Preserves current values for existing keys, adds new keys from target,
 * and removes keys that don't exist in target.
 * @param {string} targetBodyRaw - Body.raw from the target request (JSON string)
 * @param {string} sourceBodyRaw - Body.raw from the source request (JSON string)
 * @returns {string} The merged body.raw as a JSON string
 */
export declare function mergeRequestAndResponseBodyRaw(targetBodyRaw: string | undefined, sourceBodyRaw: string | undefined): string | undefined;
/**
 * Merges body.raw data in request JSON
 * @param {RequestBodyDefinition | undefined} targetBody - Target request body
 * @param {RequestBodyDefinition | undefined} sourceBody - Source request body
 * @returns {RequestBodyDefinition | undefined} Merged request body
 */
export declare function mergeRequestBodyData(targetBody: RequestBodyDefinition | undefined, sourceBody: RequestBodyDefinition | undefined): RequestBodyDefinition | undefined;
//# sourceMappingURL=BodyMerger.d.ts.map