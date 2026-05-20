export const NoChannelTrailingSlash = () => {
    return {
        Channel(channel, { report, location }) {
            if (channel?.address?.endsWith('/') && channel?.address !== '/') {
                report({
                    message: `\`${channel.address}\` should not have a trailing slash.`,
                    location: location.key(),
                });
            }
        },
    };
};
//# sourceMappingURL=no-channel-trailing-slash.js.map