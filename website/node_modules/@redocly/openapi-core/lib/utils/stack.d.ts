export type StackFrame<T> = {
    prev: StackFrame<T> | null;
    value: T;
};
export type Stack<T> = StackFrame<T> | null;
export type StackNonEmpty<T> = StackFrame<T>;
export declare function pushStack<T, P extends Stack<T> = Stack<T>>(head: P, value: T): {
    prev: P;
    value: T;
};
export declare function popStack<T, P extends Stack<T>>(head: P): StackFrame<T> | null;
//# sourceMappingURL=stack.d.ts.map