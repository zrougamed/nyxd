/**
 * Path matching utilities for Collection-related operations
 */
/**
 * Matches two identifiers by comparing their components.
 * - Exact string components must match exactly
 * - Parameter components in formats ":xyz" and "{{xyz}}" match each other at the same position
 * - Both identifiers must have the same number of components when split by "/"
 *
 * @param {string} identifier1 - First identifier to compare
 * @param {string} identifier2 - Second identifier to compare
 * @returns {boolean} True if identifiers match according to the rules, false otherwise
 */
export declare function matchIdentifiers(identifier1: string, identifier2: string): boolean;
//# sourceMappingURL=PathMatcher.d.ts.map