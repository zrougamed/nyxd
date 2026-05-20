import { isRef } from '../../ref-utils.js';
export const ArrayParameterSerialization = (options) => {
    return {
        Parameter: {
            leave(node, ctx) {
                if (!node.schema) {
                    return;
                }
                const schema = (isRef(node.schema) ? ctx.resolve(node.schema).node : node.schema);
                if (schema &&
                    shouldReportMissingStyleAndExplode(node, schema, options)) {
                    ctx.report({
                        message: `Parameter \`${node.name}\` should have \`style\` and \`explode \` fields`,
                        location: ctx.location,
                    });
                }
            },
        },
    };
};
function shouldReportMissingStyleAndExplode(node, schema, options) {
    return ((schema.type === 'array' || schema.items || schema.prefixItems) &&
        (node.style === undefined || node.explode === undefined) &&
        (!options.in || (node.in && options.in?.includes(node.in))));
}
//# sourceMappingURL=array-parameter-serialization.js.map