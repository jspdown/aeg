// Admin configuration of the self-hosted Renovate. renovate.json has the repository configuration.
module.exports = {
  platform: "github",
  repositories: ["jspdown/aeg"],
  onboarding: false,
  requireConfig: "required",
  // gomod2nix.toml must change with go.mod, or the Nix build fails.
  allowedCommands: ["^go run github\\.com/nix-community/gomod2nix@[0-9a-f]{40} --dir \\. --outdir \\.$"],
};
