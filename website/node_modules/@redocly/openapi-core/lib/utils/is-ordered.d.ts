export type OrderDirection = 'asc' | 'desc';
export type OrderOptions = {
    direction: OrderDirection;
    property: string;
};
export declare function isOrdered(value: any[], options: OrderOptions | OrderDirection): boolean;
//# sourceMappingURL=is-ordered.d.ts.map