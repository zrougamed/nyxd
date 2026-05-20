"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.mergeRequestData = mergeRequestData;
const BodyMerger_1 = require("./BodyMerger");
const HeaderMerger_1 = require("./HeaderMerger");
const UrlMerger_1 = require("./UrlMerger");
const header_1 = require("../header");
/**
 * Merges request data from source request to target request.
 * @param {RequestDefinition} targetRequest - Target request
 * @param {RequestDefinition} sourceRequest - Source request
 * @param {SyncOptions} syncOptions - Options to control what should be synced
 * @returns {RequestDefinition} Merged request
 */
function mergeRequestData(targetRequest, sourceRequest, syncOptions) {
    // Attach implicit headers from the source request to the target request if they are not present in the target request
    // Required because the request body and implicit headers are not generated
    // during collection to spec conversion for non json requests.
    (0, header_1.attachImplicitHeaders)(sourceRequest.header, targetRequest.header);
    const shouldSyncExamples = syncOptions?.syncExamples;
    if (targetRequest.header) {
        targetRequest.header = shouldSyncExamples ?
            targetRequest.header :
            (0, HeaderMerger_1.mergeRequestAndResponseHeaders)(targetRequest.header, sourceRequest?.header);
    }
    if (targetRequest.body) {
        targetRequest.body = shouldSyncExamples ?
            targetRequest.body :
            (0, BodyMerger_1.mergeRequestBodyData)(targetRequest.body, sourceRequest?.body);
    }
    targetRequest.url = shouldSyncExamples ?
        targetRequest.url :
        (0, UrlMerger_1.mergeRequestUrlData)(targetRequest.url, sourceRequest.url);
    return targetRequest;
}
//# sourceMappingURL=RequestMerger.js.map