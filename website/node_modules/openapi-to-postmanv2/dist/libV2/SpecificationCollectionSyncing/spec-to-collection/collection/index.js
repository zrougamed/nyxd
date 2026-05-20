"use strict";
/**
 * Collection Finding Utilities
 * Functions for finding and locating items within Postman collections
 */
Object.defineProperty(exports, "__esModule", { value: true });
exports.findFolderItemByName = findFolderItemByName;
exports.findRequestItemByPathAndMethod = findRequestItemByPathAndMethod;
const postman_collection_1 = require("postman-collection");
const shared_1 = require("../../shared");
const path_1 = require("../path");
/**
 * Find a folder item by name.
 * @param {ItemGroup<Item>} item - The item group to search within
 * @param {string} name - The name of the folder item to find
 * @returns {ItemGroup<Item> | undefined} The found folder item, or undefined if not found
 */
function findFolderItemByName(item, name) {
    let foundFolder;
    item.items.each((item) => {
        if (item.name === name && postman_collection_1.ItemGroup.isItemGroup(item)) {
            foundFolder = item;
            return false;
        }
        return true;
    });
    return foundFolder;
}
/**
 * Finds a request item in the given array of items by its path + method combo.
 * @param {ItemGroup<Item>} item - The item group to search within
 * @param {string} identifier - The method + path of the request item to find
 * @returns {Item | undefined} The found request item, or undefined if not found
 */
function findRequestItemByPathAndMethod(item, identifier) {
    let foundItem;
    item.items.each((item) => {
        if (item instanceof postman_collection_1.ItemGroup) {
            return true;
        }
        const currentItemIdentifier = (0, shared_1.getRequestIdentifier)(item);
        if ((0, path_1.matchIdentifiers)(currentItemIdentifier, identifier)) {
            foundItem = item;
            return false;
        }
        return true;
    });
    return foundItem;
}
//# sourceMappingURL=index.js.map