/**
 * Collection Finding Utilities
 * Functions for finding and locating items within Postman collections
 */
import { Item, ItemGroup } from 'postman-collection';
/**
 * Find a folder item by name.
 * @param {ItemGroup<Item>} item - The item group to search within
 * @param {string} name - The name of the folder item to find
 * @returns {ItemGroup<Item> | undefined} The found folder item, or undefined if not found
 */
export declare function findFolderItemByName(item: ItemGroup<Item>, name: string): ItemGroup<Item> | undefined;
/**
 * Finds a request item in the given array of items by its path + method combo.
 * @param {ItemGroup<Item>} item - The item group to search within
 * @param {string} identifier - The method + path of the request item to find
 * @returns {Item | undefined} The found request item, or undefined if not found
 */
export declare function findRequestItemByPathAndMethod(item: ItemGroup<Item>, identifier: string): Item | undefined;
//# sourceMappingURL=index.d.ts.map