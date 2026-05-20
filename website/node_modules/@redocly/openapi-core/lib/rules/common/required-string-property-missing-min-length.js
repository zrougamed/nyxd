export const RequiredStringPropertyMissingMinLength = () => {
    let skipSchemaProperties;
    let requiredPropertiesSet;
    return {
        Schema: {
            enter(schema) {
                if (!schema?.required) {
                    skipSchemaProperties = true;
                    return;
                }
                requiredPropertiesSet = new Set(schema.required);
                skipSchemaProperties = false;
            },
            SchemaProperties: {
                skip() {
                    return skipSchemaProperties;
                },
                Schema: {
                    enter(schema, { key, location, report }) {
                        if (requiredPropertiesSet.has(key) && schema.type === 'string') {
                            if (!schema?.minLength) {
                                report({
                                    message: 'Property minLength is required.',
                                    location: location.key(),
                                });
                            }
                        }
                    },
                },
            },
        },
    };
};
//# sourceMappingURL=required-string-property-missing-min-length.js.map