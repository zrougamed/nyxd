import { isRef } from '../../ref-utils.js';
export const SpecStrictRefs = () => {
    const nodesToSkip = [
        'Schema',
        'Response',
        'Parameter',
        'RequestBody',
        'Example',
        'Header',
        'SecurityScheme',
        'Link',
        'Callback',
        'PathItem',
    ];
    return {
        any(_node, { report, rawNode, rawLocation, type }) {
            const shouldCheck = !nodesToSkip.includes(type.name);
            if (shouldCheck && isRef(rawNode)) {
                report({
                    message: 'Field $ref is not expected here.',
                    location: rawLocation.child('$ref').key(),
                });
            }
        },
    };
};
//# sourceMappingURL=spec-strict-refs.js.map