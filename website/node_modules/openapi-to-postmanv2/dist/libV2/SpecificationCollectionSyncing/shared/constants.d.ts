/**
 * Common constants used across SpecificationCollection modules
 */
import { SyncOptions } from './types';
/**
 * Number of spaces for JSON formatting
 */
export declare const SPACE_COUNT = 2;
/**
 * Default response code used in OpenAPI specifications
 */
export declare const DEFAULT_RESPONSE_CODE_IN_OAS = "default";
/**
 * Authentication types supported in the system
 */
export declare const AUTH_TYPES: {
    readonly BASIC: "basic";
    readonly BEARER: "bearer";
    readonly DIGEST: "digest";
    readonly API_KEY: "apikey";
    readonly OAUTH2: "oauth2";
};
/**
 * Allowed authentication parameter keys by authentication type
 */
export declare const ALLOWED_AUTH_PARAM_KEYS_BY_TYPE: Record<string, Set<string>>;
export declare const DEFAULT_SYNC_OPTIONS: SyncOptions;
//# sourceMappingURL=constants.d.ts.map