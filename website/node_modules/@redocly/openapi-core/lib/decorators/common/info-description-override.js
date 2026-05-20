import { readFileAsStringSync, resolveRelativePath } from '../../utils/yaml-fs-helper.js';
export const InfoDescriptionOverride = ({ filePath }) => {
    return {
        Info: {
            leave(info, { report, location, config }) {
                if (!filePath)
                    throw new Error(`Parameter "filePath" is not provided for "info-description-override" rule`);
                try {
                    info.description = readFileAsStringSync(resolveRelativePath(filePath, config?.configPath));
                }
                catch (e) {
                    report({
                        message: `Failed to read markdown override file for "info.description".\n${e.message}`,
                        location: location.child('description'),
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=info-description-override.js.map