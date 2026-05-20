export const PathDeclarationMustExist = () => {
    return {
        PathItem(_path, { report, key }) {
            if (key.indexOf('{}') !== -1) {
                report({
                    message: 'Path parameter declarations must be non-empty. `{}` is invalid.',
                    location: { reportOnKey: true },
                });
            }
        },
    };
};
//# sourceMappingURL=path-declaration-must-exist.js.map