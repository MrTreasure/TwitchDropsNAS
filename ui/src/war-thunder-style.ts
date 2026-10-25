import { theme, type ThemeConfig } from 'antd';

// Original official assets, loaded directly; never proxy or copy them into a deployment.
export const warThunderAssets = {
 logo: 'https://warthunder.com/assets/img/svg/logo-wt.svg',
 icon: 'https://warthunder.com/i/favicons/favicon.ico',
 research: 'https://static.encyclopedia.warthunder.com/gui_skin/item_type_rp.svg',
 talisman: 'https://static.encyclopedia.warthunder.com/gui_skin/item_type_talisman.svg',
} as const;

// Palette source: the War Thunder Workbench visual specification.
// Scope this provider to the Drops page; Ant Design portals inherit its tokens.
export const warThunderDropsTheme: ThemeConfig = {
 algorithm: theme.darkAlgorithm,
 token: {
  colorPrimary: '#b33f32', colorPrimaryHover: '#c64c3e', colorPrimaryActive: '#96352b',
  colorTextBase: '#efede5', colorText: '#cfd1cf', colorTextHeading: '#efede5',
  colorTextSecondary: '#a1a7aa', colorTextDescription: '#a1a7aa', colorTextTertiary: '#a1a7aa', colorTextDisabled: '#7c858d',
  colorBgBase: '#171c22', colorBgLayout: '#171c22', colorBgContainer: '#22282f', colorBgElevated: '#30373e',
  colorBgContainerDisabled: '#30373e', colorBorder: 'rgba(187,199,207,.14)', colorBorderSecondary: 'rgba(187,199,207,.14)',
  colorLink: '#e4c477', colorLinkHover: '#f0d99d', colorSuccess: '#a9cfaf', colorInfo: '#a9c5df', colorWarning: '#e4c477', colorError: '#f0a39a',
  borderRadius: 3, fontSize: 13, fontFamily: 'Inter, system-ui, "Microsoft YaHei", sans-serif',
 },
 components: {
  Button: { primaryColor: '#efede5', primaryShadow: 'none', defaultShadow: 'none' },
  Table: { headerBg: '#30373e', headerColor: '#a1a7aa', colorBgContainer: '#22282f', rowHoverBg: '#30373e', rowSelectedBg: '#453b2b', rowSelectedHoverBg: '#55472f', cellPaddingBlockSM: 12 },
  Select: { optionSelectedBg: '#453b2b', optionSelectedColor: '#e4c477', optionActiveBg: '#3c454e' },
  Input: { activeBorderColor: '#e4c477', activeShadow: '0 0 0 2px rgba(228,196,119,.2)' },
  Progress: { defaultColor: '#e4c477' },
  Checkbox: { colorPrimary: '#e4c477', colorPrimaryHover: '#f0d99d', colorWhite: '#171c22' },
 },
};
