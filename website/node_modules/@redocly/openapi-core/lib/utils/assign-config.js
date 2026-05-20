import { isPlainObject } from './is-plain-object.js';
export const assignConfig = (target, obj) => {
    if (!obj)
        return;
    for (const k of Object.keys(obj)) {
        if (isPlainObject(target[k]) && typeof obj[k] === 'string') {
            target[k] = { ...target[k], severity: obj[k] };
        }
        else {
            target[k] = obj[k];
        }
    }
};
export function assignOnlyExistingConfig(target, obj) {
    if (!obj)
        return;
    for (const k of Object.keys(obj)) {
        if (!target.hasOwnProperty(k))
            continue;
        if (isPlainObject(target[k]) && typeof obj[k] === 'string') {
            target[k] = { ...target[k], severity: obj[k] };
        }
        else {
            target[k] = obj[k];
        }
    }
}
//# sourceMappingURL=assign-config.js.map