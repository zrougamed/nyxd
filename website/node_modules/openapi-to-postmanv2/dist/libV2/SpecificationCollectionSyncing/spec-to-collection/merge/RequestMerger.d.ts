import { RequestDefinition } from 'postman-collection';
import { SyncOptions } from '../../shared';
/**
 * Merges request data from source request to target request.
 * @param {RequestDefinition} targetRequest - Target request
 * @param {RequestDefinition} sourceRequest - Source request
 * @param {SyncOptions} syncOptions - Options to control what should be synced
 * @returns {RequestDefinition} Merged request
 */
export declare function mergeRequestData(targetRequest: RequestDefinition, sourceRequest: RequestDefinition, syncOptions: SyncOptions): RequestDefinition;
//# sourceMappingURL=RequestMerger.d.ts.map