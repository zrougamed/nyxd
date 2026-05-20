export const NullableTypeSibling = () => {
    return {
        Schema: {
            leave(schema, ctx) {
                if ('nullable' in schema && !('type' in schema)) {
                    ctx.report({
                        message: `The \`type\` field must be defined when the \`nullable\` field is used.`,
                        location: ctx.location.child(['nullable']),
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=nullable-type-sibling.js.map