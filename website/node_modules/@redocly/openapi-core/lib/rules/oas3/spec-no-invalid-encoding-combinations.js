export const SpecNoInvalidEncodingCombinations = () => {
    return {
        MediaType: {
            leave(mediaType, ctx) {
                if (('prefixEncoding' in mediaType && 'encoding' in mediaType) ||
                    ('itemEncoding' in mediaType && 'encoding' in mediaType)) {
                    ctx.report({
                        message: "The 'encoding' field cannot be used together with 'prefixEncoding' or 'itemEncoding'.",
                        location: ctx.location.child('encoding').key(),
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=spec-no-invalid-encoding-combinations.js.map