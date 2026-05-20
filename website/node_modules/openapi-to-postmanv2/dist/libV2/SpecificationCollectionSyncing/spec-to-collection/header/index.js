"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.attachImplicitHeaders = attachImplicitHeaders;
const postman_collection_1 = require("postman-collection");
/**
 * Update the implicit headers from the current collection if the latest collection does not have the implicit headers.
 * @param {HeaderDefinition[] | undefined} currentHeaders - The headers from the current collection
 * @param {HeaderDefinition[] | undefined} latestHeaders - The headers from the latest collection
 */
function attachImplicitHeaders(currentHeaders, latestHeaders) {
    const implicitHeaders = ['Accept', 'Content-Type'];
    if (!currentHeaders || !latestHeaders) {
        return;
    }
    currentHeaders.forEach((currentHeader) => {
        if (!currentHeader.key || !implicitHeaders.includes(currentHeader.key)) {
            return;
        }
        const hasSameKeyInLatestHeaders = latestHeaders.some((header) => {
            return header.key === currentHeader.key;
        });
        if (!hasSameKeyInLatestHeaders) {
            latestHeaders.push(new postman_collection_1.Header(currentHeader));
        }
    });
}
//# sourceMappingURL=index.js.map