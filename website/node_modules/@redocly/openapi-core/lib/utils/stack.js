export function pushStack(head, value) {
    return { prev: head, value };
}
export function popStack(head) {
    return head?.prev ?? null;
}
//# sourceMappingURL=stack.js.map