import { isEmptyObject } from './is-empty-object.js';
import { isPlainObject } from './is-plain-object.js';
export function isNotEmptyObject(obj) {
    return isPlainObject(obj) && !isEmptyObject(obj);
}
//# sourceMappingURL=is-not-empty-object.js.map