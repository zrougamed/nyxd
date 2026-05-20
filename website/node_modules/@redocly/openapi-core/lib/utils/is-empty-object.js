import { isPlainObject } from './is-plain-object.js';
export function isEmptyObject(value) {
    return isPlainObject(value) && Object.keys(value).length === 0;
}
//# sourceMappingURL=is-empty-object.js.map