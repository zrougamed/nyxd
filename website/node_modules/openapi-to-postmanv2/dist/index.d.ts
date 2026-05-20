import type { SpecificationInput, Options, Callback, SyncOptions, OptionsCriteria, OptionDefinition, OptionsRecord, BundleResult, FilesResult, ValidationResult } from './types';
export type { Options, SyncOptions, Callback, CollectionResult, BundleResult, FilesResult, ValidationResult } from './types';
declare const SchemaPack: any;
/**
 * Converts an OpenAPI specification to a Postman Collection (v1 API)
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
export declare function convert(input: SpecificationInput, options: Options, cb: Callback): void;
/**
 * Converts an OpenAPI specification to a Postman Collection (v2 API)
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
export declare function convertV2(input: SpecificationInput, options: Options, cb: Callback): void;
/**
 * Converts an OpenAPI specification to a Postman Collection (v2 API) with type fetching enabled
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
export declare function convertV2WithTypes(input: SpecificationInput, options: Options, cb: Callback): void;
/**
 * Validates an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {ValidationResult} Validation result
 */
export declare function validate(input: SpecificationInput): ValidationResult;
/**
 * Retrieves metadata from an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Callback} cb - Callback function with metadata result
 * @returns {void}
 */
export declare function getMetaData(input: SpecificationInput, cb: Callback): void;
/**
 * Merges and validates an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Callback} cb - Callback function with merge and validation result
 * @returns {void}
 */
export declare function mergeAndValidate(input: SpecificationInput, cb: Callback): void;
/**
 * Gets conversion options
 * @param {string} mode - Mode for options ('document' or 'use')
 * @param {OptionsCriteria} criteria - Criteria for filtering options
 * @returns {OptionDefinition[] | OptionsRecord} Option definitions or option values
 */
export declare function getOptions(mode?: string, criteria?: OptionsCriteria): OptionDefinition[] | OptionsRecord;
/**
 * Gets sync options
 * @param {string} mode - Mode for options ('document' or 'use')
 * @returns {OptionDefinition[] | OptionsRecord} Option definitions or option values
 */
export declare function getSyncOptions(mode?: string): OptionDefinition[] | OptionsRecord;
/**
 * Detects root files in a multi-file OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {Promise<FilesResult>} Promise with detection result
 */
export declare function detectRootFiles(input: SpecificationInput): Promise<FilesResult>;
/**
 * Detects related files in a multi-file OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {Promise<FilesResult>} Promise with detection result
 */
export declare function detectRelatedFiles(input: SpecificationInput): Promise<FilesResult>;
/**
 * Bundles a multi-file OpenAPI specification into a single file
 * @param {SpecificationInput} input - The OpenAPI specification input with optional bundling options
 * @returns {Promise<BundleResult>} Promise with bundled specification
 */
export declare function bundle(input: SpecificationInput & {
    options?: Options;
}): Promise<BundleResult>;
/**
 * Syncs a Postman Collection with changes from an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {object} currentCollection - The current Postman Collection to sync
 * @param {SyncOptions | null} syncOptions - Sync options
 * @param {Callback} cb - Callback function with sync result
 * @returns {void}
 */
export declare function syncCollection(input: SpecificationInput, options: Options, currentCollection: object, syncOptions: SyncOptions | null, cb: Callback): void;
export { SchemaPack };
declare const _default: {
    convert: typeof convert;
    convertV2: typeof convertV2;
    convertV2WithTypes: typeof convertV2WithTypes;
    validate: typeof validate;
    getMetaData: typeof getMetaData;
    mergeAndValidate: typeof mergeAndValidate;
    getOptions: typeof getOptions;
    getSyncOptions: typeof getSyncOptions;
    detectRootFiles: typeof detectRootFiles;
    detectRelatedFiles: typeof detectRelatedFiles;
    bundle: typeof bundle;
    syncCollection: typeof syncCollection;
    SchemaPack: any;
};
export default _default;
//# sourceMappingURL=index.d.ts.map