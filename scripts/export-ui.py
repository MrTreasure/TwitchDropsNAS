"""Copy only the reusable UI into a host frontend, never import host source."""
import argparse
import shutil
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('destination', type=Path)
args = parser.parse_args()
source = Path(__file__).resolve().parents[1] / 'ui/src'
args.destination.mkdir(parents=True, exist_ok=True)
shutil.copy2(source / 'TwitchDrops.tsx', args.destination / 'TwitchDropsView.tsx')
shutil.copy2(source / 'twitch-drops.css', args.destination / 'twitch-drops.css')
print('Exported reusable component and CSS; host adapter is unchanged.')
