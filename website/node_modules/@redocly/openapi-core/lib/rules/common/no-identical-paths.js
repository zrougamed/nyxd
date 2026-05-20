export const NoIdenticalPaths = () => {
    return {
        Paths(pathMap, { report, location }) {
            const Paths = new Map();
            for (const pathName of Object.keys(pathMap)) {
                const id = pathName.replace(/{.+?}/g, '{VARIABLE}');
                const existingSamePath = Paths.get(id);
                if (existingSamePath) {
                    report({
                        message: `The path already exists which differs only by path parameter name(s): \`${existingSamePath}\` and \`${pathName}\`.`,
                        location: location.child([pathName]).key(),
                    });
                }
                else {
                    Paths.set(id, pathName);
                }
            }
        },
    };
};
//# sourceMappingURL=no-identical-paths.js.map