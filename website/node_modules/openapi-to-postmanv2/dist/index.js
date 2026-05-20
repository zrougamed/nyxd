'use strict';
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.SchemaPack = void 0;
exports.convert = convert;
exports.convertV2 = convertV2;
exports.convertV2WithTypes = convertV2WithTypes;
exports.validate = validate;
exports.getMetaData = getMetaData;
exports.mergeAndValidate = mergeAndValidate;
exports.getOptions = getOptions;
exports.getSyncOptions = getSyncOptions;
exports.detectRootFiles = detectRootFiles;
exports.detectRelatedFiles = detectRelatedFiles;
exports.bundle = bundle;
exports.syncCollection = syncCollection;
const lodash_1 = __importDefault(require("lodash"));
const { MODULE_VERSION } = require('../lib/schemapack.js');
const SchemaPack = require('../lib/schemapack.js').SchemaPack;
exports.SchemaPack = SchemaPack;
const UserError = require('../lib/common/UserError');
const DEFAULT_INVALID_ERROR = 'Provided definition is invalid';
/**
 * Converts an OpenAPI specification to a Postman Collection (v1 API)
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
function convert(input, options, cb) {
    var schema = new SchemaPack(input, options);
    if (schema.validated) {
        return schema.convert(cb);
    }
    return cb(new UserError(lodash_1.default.get(schema, 'validationResult.reason', DEFAULT_INVALID_ERROR)));
}
/**
 * Converts an OpenAPI specification to a Postman Collection (v2 API)
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
function convertV2(input, options, cb) {
    var schema = new SchemaPack(input, options, MODULE_VERSION.V2);
    if (schema.validated) {
        return schema.convertV2(cb);
    }
    return cb(new UserError(lodash_1.default.get(schema, 'validationResult.reason', DEFAULT_INVALID_ERROR)));
}
/**
 * Converts an OpenAPI specification to a Postman Collection (v2 API) with type fetching enabled
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {Callback} cb - Callback function with conversion result
 * @returns {void}
 */
function convertV2WithTypes(input, options, cb) {
    const enableTypeFetching = true;
    var schema = new SchemaPack(input, options, MODULE_VERSION.V2, enableTypeFetching);
    if (schema.validated) {
        return schema.convertV2(cb);
    }
    return cb(new UserError(lodash_1.default.get(schema, 'validationResult.reason', DEFAULT_INVALID_ERROR)));
}
/**
 * Validates an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {ValidationResult} Validation result
 */
function validate(input) {
    var schema = new SchemaPack(input);
    return schema.validationResult;
}
/**
 * Retrieves metadata from an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Callback} cb - Callback function with metadata result
 * @returns {void}
 */
function getMetaData(input, cb) {
    var schema = new SchemaPack(input);
    schema.getMetaData(cb);
}
/**
 * Merges and validates an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Callback} cb - Callback function with merge and validation result
 * @returns {void}
 */
function mergeAndValidate(input, cb) {
    var schema = new SchemaPack(input);
    schema.mergeAndValidate(cb);
}
/**
 * Gets conversion options
 * @param {string} mode - Mode for options ('document' or 'use')
 * @param {OptionsCriteria} criteria - Criteria for filtering options
 * @returns {OptionDefinition[] | OptionsRecord} Option definitions or option values
 */
function getOptions(mode, criteria) {
    return SchemaPack.getOptions(mode, criteria);
}
/**
 * Gets sync options
 * @param {string} mode - Mode for options ('document' or 'use')
 * @returns {OptionDefinition[] | OptionsRecord} Option definitions or option values
 */
function getSyncOptions(mode) {
    return SchemaPack.getSyncOptions(mode);
}
/**
 * Detects root files in a multi-file OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {Promise<FilesResult>} Promise with detection result
 */
async function detectRootFiles(input) {
    var schema = new SchemaPack(input);
    return schema.detectRootFiles();
}
/**
 * Detects related files in a multi-file OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @returns {Promise<FilesResult>} Promise with detection result
 */
async function detectRelatedFiles(input) {
    var schema = new SchemaPack(input);
    return schema.detectRelatedFiles();
}
/**
 * Bundles a multi-file OpenAPI specification into a single file
 * @param {SpecificationInput} input - The OpenAPI specification input with optional bundling options
 * @returns {Promise<BundleResult>} Promise with bundled specification
 */
async function bundle(input) {
    var schema = new SchemaPack(input, input.options ?? {});
    return schema.bundle();
}
/**
 * Syncs a Postman Collection with changes from an OpenAPI specification
 * @param {SpecificationInput} input - The OpenAPI specification input
 * @param {Options} options - Conversion options
 * @param {object} currentCollection - The current Postman Collection to sync
 * @param {SyncOptions | null} syncOptions - Sync options
 * @param {Callback} cb - Callback function with sync result
 * @returns {void}
 */
function syncCollection(input, options, currentCollection, syncOptions, cb) {
    const enableTypeFetching = true;
    var schema = new SchemaPack(input, options, MODULE_VERSION.V2, enableTypeFetching);
    if (schema.validated) {
        return schema.syncCollection(currentCollection, syncOptions, cb);
    }
    return cb(new UserError(lodash_1.default.get(schema, 'validationResult.reason', DEFAULT_INVALID_ERROR)));
}
exports.default = {
    convert,
    convertV2,
    convertV2WithTypes,
    validate,
    getMetaData,
    mergeAndValidate,
    getOptions,
    getSyncOptions,
    detectRootFiles,
    detectRelatedFiles,
    bundle,
    syncCollection,
    SchemaPack
};
//# sourceMappingURL=index.js.map