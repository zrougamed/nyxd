import { isTruthy } from './is-truthy.js';
export function splitCamelCaseIntoWords(str) {
    const camel = str
        .split(/(?:[-._])|([A-Z][a-z]+)/)
        .filter(isTruthy)
        .map((item) => item.toLocaleLowerCase());
    const caps = str
        .split(/([A-Z]{2,})/)
        .filter((e) => e && e === e.toUpperCase())
        .map((item) => item.toLocaleLowerCase());
    return new Set([...camel, ...caps]);
}
//# sourceMappingURL=split-camel-case-into-words.js.map