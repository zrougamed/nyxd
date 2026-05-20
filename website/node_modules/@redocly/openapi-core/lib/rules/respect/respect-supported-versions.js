import { ARAZZO_VERSIONS_SUPPORTED_BY_RESPECT } from '../../typings/arazzo.js';
import { pluralize } from '../../utils/pluralize.js';
export const RespectSupportedVersions = () => {
    const supportedVersions = ARAZZO_VERSIONS_SUPPORTED_BY_RESPECT.join(', ');
    return {
        Root: {
            enter(root, { report, location }) {
                if (!ARAZZO_VERSIONS_SUPPORTED_BY_RESPECT.includes(root.arazzo)) {
                    report({
                        message: `Only ${supportedVersions} Arazzo ${pluralize('version is', ARAZZO_VERSIONS_SUPPORTED_BY_RESPECT.length)} fully supported by Respect.`,
                        location: location.child('arazzo'),
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=respect-supported-versions.js.map