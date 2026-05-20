export function getIntersectionLength(keys, properties) {
    const props = new Set(properties);
    let count = 0;
    for (const key of keys) {
        if (props.has(key)) {
            count++;
        }
    }
    return count;
}
//# sourceMappingURL=get-intersection-length.js.map