"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
exports.convertAuthParamsToVariableList = convertAuthParamsToVariableList;
exports.mergeAllowedAuthParams = mergeAllowedAuthParams;
exports.mergeAuth = mergeAuth;
exports.mergeAuthParams = mergeAuthParams;
const lodash_1 = __importDefault(require("lodash"));
const postman_collection_1 = require("postman-collection");
const shared_1 = require("../../shared");
/**
 * Convert VariableDefinition array to a VariableList
 * @param {VariableDefinition[] | undefined} params - Variable definitions from auth
 * @returns {VariableList | undefined} VariableList instance or undefined
 */
function convertAuthParamsToVariableList(params) {
    if (!params || params.length === 0) {
        return undefined;
    }
    const variables = [];
    for (const p of params) {
        if (p && typeof p.key === 'string') {
            variables.push(new postman_collection_1.Variable(p));
        }
    }
    if (variables.length === 0) {
        return undefined;
    }
    return new postman_collection_1.VariableList(null, variables);
}
/**
 * Merge only allowed auth parameter keys for the given auth type into result.
 *
 * @param {string|undefined} latestType Auth type key (e.g., apikey, oauth2)
 * @param {RequestAuthDefinition} latest Latest auth JSON
 * @param {RequestAuthDefinition} currentAuthObject Current/target auth JSON (mutated)
 */
function mergeAllowedAuthParams(latestType, latest, currentAuthObject) {
    const allowedKeys = shared_1.ALLOWED_AUTH_PARAM_KEYS_BY_TYPE[latestType ?? ''];
    if (!latestType || !allowedKeys || allowedKeys.size === 0) {
        return currentAuthObject;
    }
    const latestParamsArray = latest[latestType];
    if (!Array.isArray(latestParamsArray)) {
        return currentAuthObject;
    }
    // Deep clone the current auth object to avoid mutating the original object
    const result = lodash_1.default.cloneDeep(currentAuthObject), currentParamsArrayRaw = result[latestType], currentParamsArray = Array.isArray(currentParamsArrayRaw) ? currentParamsArrayRaw : [], currentByKey = new Map();
    for (const p of currentParamsArray) {
        if (p && typeof p.key === 'string') {
            currentByKey.set(p.key, p);
        }
    }
    for (const param of latestParamsArray) {
        const key = typeof param?.key === 'string' ? param.key : undefined;
        if (!key || !allowedKeys.has(key)) {
            continue;
        }
        const existing = currentByKey.get(key);
        if (existing) {
            existing.value = param.value;
            if (param.type !== undefined) {
                existing.type = param.type;
            }
        }
        else {
            currentParamsArray.push({ key, value: param.value, type: param.type });
        }
    }
    if (latestType === shared_1.AUTH_TYPES.API_KEY) {
        result.apikey = currentParamsArray;
    }
    else if (latestType === shared_1.AUTH_TYPES.OAUTH2) {
        result.oauth2 = currentParamsArray;
    }
    return result;
}
/**
 * Build merged auth JSON without overwriting user secrets.
 *
 * @param {RequestAuthDefinition} latestAuth Auth from the latest collection (derived from spec)
 * @param {RequestAuthDefinition} currentAuth Auth from the current collection (may contain user values)
 */
function mergeAuth(latestAuth, currentAuth) {
    if (!latestAuth || typeof latestAuth !== 'object') {
        return currentAuth;
    }
    const currentAuthObject = currentAuth && typeof currentAuth === 'object' ? currentAuth : {}, latestType = latestAuth?.type;
    if (latestType) {
        currentAuthObject.type = latestType;
    }
    if (latestType === shared_1.AUTH_TYPES.API_KEY || latestType === shared_1.AUTH_TYPES.OAUTH2) {
        return mergeAllowedAuthParams(latestType, latestAuth, currentAuthObject);
    }
    // TODO: Remove other auth types from the result if they are present in the older collection.
    // It works in the present state as well since Postman itself does handle the "active auth" automatically
    //  based on the type field.
    return currentAuthObject;
}
/**
 * Merge authentication parameters for application.
 * Returns the auth type and parameters to be used, preserving user credentials where appropriate.
 *
 * @param {RequestAuthDefinition} mergedAuth - The merged authentication configuration
 * @param {RequestAuthDefinition} existingAuth - The existing authentication (may contain user secrets)
 *
 * @returns {{ type: string; params: VariableList | undefined } | null}
 * Object with auth type and params, or null if no auth
 */
function mergeAuthParams(mergedAuth, existingAuth) {
    const latestType = mergedAuth?.type;
    if (!latestType) {
        return null;
    }
    if (latestType === shared_1.AUTH_TYPES.BASIC || latestType === shared_1.AUTH_TYPES.BEARER || latestType === shared_1.AUTH_TYPES.DIGEST) {
        const preservedParams = existingAuth?.basic || existingAuth?.bearer || existingAuth?.digest;
        return { type: latestType, params: convertAuthParamsToVariableList(preservedParams) };
    }
    if (latestType === shared_1.AUTH_TYPES.API_KEY || latestType === shared_1.AUTH_TYPES.OAUTH2) {
        return { type: latestType, params: convertAuthParamsToVariableList(mergedAuth[latestType]) };
    }
    return null;
}
//# sourceMappingURL=index.js.map