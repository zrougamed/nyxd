import { readFileAsStringSync, resolveRelativePath } from '../../utils/yaml-fs-helper.js';
export const TagDescriptionOverride = ({ tagNames }) => {
    return {
        Tag: {
            leave(tag, { report, config }) {
                if (!tagNames)
                    throw new Error(`Parameter "tagNames" is not provided for "tag-description-override" rule`);
                const filePath = tagNames[tag.name];
                if (filePath) {
                    try {
                        tag.description = readFileAsStringSync(resolveRelativePath(filePath, config?.configPath));
                    }
                    catch (e) {
                        report({
                            message: `Failed to read markdown override file for tag "${tag.name}".\n${e.message}`,
                        });
                    }
                }
            },
        },
    };
};
//# sourceMappingURL=tag-description-override.js.map