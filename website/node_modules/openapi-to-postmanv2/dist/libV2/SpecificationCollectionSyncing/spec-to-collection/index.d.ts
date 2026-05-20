import { Collection, ItemGroup, Item } from 'postman-collection';
import { SyncOptions } from '../shared';
/**
 * Syncs the state between two item groups / Collections.
 *
 * Warning: This mutates the current collection
 * @param {ItemGroup} latestCollectionState - The item group to be synced.
 * @param {ItemGroup} currentCollectionState - The current item group.
 * @param {SyncOptions} options - Options to control what should be synced.
 */
export declare function syncCollection(latestCollectionState: Collection | ItemGroup<Item>, currentCollectionState: Collection | ItemGroup<Item>, options: SyncOptions | null): ItemGroup<Item>;
//# sourceMappingURL=index.d.ts.map