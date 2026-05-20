import { RequestAuthDefinition, VariableDefinition, VariableList } from 'postman-collection';
/**
 * Convert VariableDefinition array to a VariableList
 * @param {VariableDefinition[] | undefined} params - Variable definitions from auth
 * @returns {VariableList | undefined} VariableList instance or undefined
 */
export declare function convertAuthParamsToVariableList(params: VariableDefinition[] | undefined): VariableList | undefined;
/**
 * Merge only allowed auth parameter keys for the given auth type into result.
 *
 * @param {string|undefined} latestType Auth type key (e.g., apikey, oauth2)
 * @param {RequestAuthDefinition} latest Latest auth JSON
 * @param {RequestAuthDefinition} currentAuthObject Current/target auth JSON (mutated)
 */
export declare function mergeAllowedAuthParams(latestType: keyof RequestAuthDefinition | undefined, latest: RequestAuthDefinition, currentAuthObject: RequestAuthDefinition): RequestAuthDefinition;
/**
 * Build merged auth JSON without overwriting user secrets.
 *
 * @param {RequestAuthDefinition} latestAuth Auth from the latest collection (derived from spec)
 * @param {RequestAuthDefinition} currentAuth Auth from the current collection (may contain user values)
 */
export declare function mergeAuth(latestAuth: RequestAuthDefinition, currentAuth: RequestAuthDefinition): RequestAuthDefinition;
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
export declare function mergeAuthParams(mergedAuth: RequestAuthDefinition | undefined, existingAuth: RequestAuthDefinition | undefined): {
    type: string;
    params: VariableList | undefined;
} | null;
//# sourceMappingURL=index.d.ts.map