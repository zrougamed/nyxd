export const ChannelsKebabCase = () => {
    return {
        Channel(channel, { report }) {
            const segments = (channel.address || '')
                .split(/[/.:]/) // split on / or : as likely channel namespacers
                .filter((s) => s !== ''); // filter out empty segments
            if (!segments.every((segment) => /^{.+}$/.test(segment) || /^[a-z0-9-.]+$/.test(segment))) {
                report({
                    message: `\`${channel.address}\` does not use kebab-case.`,
                    location: { reportOnKey: true },
                });
            }
        },
    };
};
//# sourceMappingURL=channels-kebab-case.js.map