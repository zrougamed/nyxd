import { getOwn } from '../../utils/get-own.js';
const SCALAR_TYPES = ['string', 'integer', 'number', 'boolean', 'null'];
export const ScalarPropertyMissingExample = () => {
    return {
        SchemaProperties(properties, { report, location, specVersion, resolve }) {
            for (const propName of Object.keys(properties)) {
                const propSchema = resolve(getOwn(properties, propName)).node;
                if (!propSchema || !isScalarSchema(propSchema)) {
                    continue;
                }
                if (propSchema.example === undefined &&
                    propSchema.examples === undefined) {
                    report({
                        message: `Scalar property should have "example"${specVersion === 'oas3_1' || specVersion === 'oas3_2' ? ' or "examples"' : ''} defined.`,
                        location: location.child(propName).key(),
                    });
                }
            }
        },
    };
};
function isScalarSchema(schema) {
    if (!schema.type) {
        return false;
    }
    if (schema.allOf || schema.anyOf || schema.oneOf) {
        // Skip allOf/oneOf/anyOf as it's complicated to validate it right now.
        // We need core support for checking contrstrains through those keywords.
        return false;
    }
    if (schema.format === 'binary') {
        return false;
    }
    if (Array.isArray(schema.type)) {
        return schema.type.every((t) => SCALAR_TYPES.includes(t));
    }
    return SCALAR_TYPES.includes(schema.type);
}
//# sourceMappingURL=scalar-property-missing-example.js.map