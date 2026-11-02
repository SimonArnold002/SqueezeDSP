package Plugins::SqueezeDSP::Plugin;
=pod version history
	# ----------------------------------------------------------------------------
	# SqueezeDSP\Plugin.pm - a SlimServer plugin.
	# Makes a remote-control user interface, and writes settings files, which
	# provide parameters for operation of a convolution and filter engine.
	
	# ----------------------------------------------------------------------------
	#
	#
	#
	# Revision history:
	#
	#
	#
	0.1.72.25	Fork: Upsample's passband defaults to 99% per family, and to 95% with Player max, which takes no
				higher (a higher choice plays at 95%); Auto stays a choice. The classic page shows a stored Auto and a
				DSD low-pass of None as they are.
	0.1.72.24	Fork: our DSD decoder takes a DSF to every target, the 48 kHz family (Upsample's fixed 96k, 384k, 768k)
				and 705.6k from DSD64 included, in place of ffmpeg's soxr.
	0.1.72.23	Fork: the engine decodes DSF itself (our own decoder, in place of ffmpeg) for a DSD target up to
				352.8 kHz, with a DSD low-pass (24/30/50 kHz or none) and gain (0/+6 dB) set per player; DFF keeps its route.
	0.1.72.22	Fork: the transcode conf is written whole to a file of its own and renamed into place; a player listed
				twice in it is written once, with a warning naming it (its rules were held twice).
	0.1.72.21	Fork: DSD plays through the DSP on a server with no ffmpeg (DSDPlayer's dsdplay decodes it where that
				plugin is enabled, seeking at once; LMS's own sox otherwise); a reduced stream
				keeps the DSP at the track's own rate, never upsampled: MP3 at a player's Bitrate Limiting, and an
				app's Opus, Ogg or MP3 (LyrPlay's lossy modes); the page no longer says "not in this player's signal
				path" for the last seconds of a queue.
	0.1.72.20	Fork, review fixes: an album gain with no album peak is capped by the track's own peak; a PCM-mode
				player's FLAC rules (taken under Bitrate Limiting) stop at FLAC's 384k, so MP3 and DSD play there again;
				a WAV or AIFF stream with Upsample reads its pipe as pipe:0; the SqueezeDSP page keeps a change made
				while a save was out when another player is picked, and no longer searches every process while a
				player is idle (Linux x64 engine).
	0.1.72.19	Fork: a FIR filter keeps its level at every rate. Played at a rate other than its own (with Upsample,
				a 96 kHz filter at 384 kHz) it was up to 12 dB too loud, so ReplayGain levels changed with the rate, FIR
				Strength mixed the wrong amounts, and the log's internal gain read far too low. ReplayGain is now capped by
				the track's or album's peak, as LMS caps it, so a positive gain no longer clips (Linux x64 engine).
	0.1.72.18	Fork: with PCM (WAV) out Upsample goes up to 768k (FLAC out stays at 384k), and a Max rate caps it on
				both pages (No limit, 768, 384, 192 or 96 kHz), for every track and DSD.
	0.1.72.17	Fork: DSD reaches its rate through ffmpeg's soxr when that ffmpeg has it (the player's Upsample options,
				precision 28 with Upsample off), not ffmpeg's own resampler; the SqueezeDSP page shows it. A DSD track is
				no longer called bypassed.
	0.1.72.16	Fork: a track already at its target rate skips Upsample's resampler and plays as with Upsample off; the
				SqueezeDSP page says it was bypassed.
	0.1.72.15	Fork: Upsample's target rate: every track, DSD included, to the highest rate the player reports, so it never
				changes rate, or each track to the top rate of its own family as before. The SqueezeDSP page finds a local
				file's pipeline.
	0.1.72.14	Fork: the classic settings page: Upsample on and off with the same switch as DSP and Replay Gain.
	0.1.72.13	Fork: the classic settings page: Signal under Output, the upsample rates beside its buttons, Precision and
				Passband as buttons.
	0.1.72.12	Fork: the classic settings page on a phone in Material: no white blocks round the section headers; the Output
				buttons line up with the other controls.
	0.1.72.11	Fork: the classic settings page re-laid out: Output and Upsample each in their own section, one option per line,
				the Steep filter explained, and the page fits a phone or a tablet.
	0.1.72.10	Fork: our own build of the engine on Linux x64: a WAV or AIFF file plays to its end, and PCM mode's WAV
				length is written by the engine. Seeking a local MP3, AAC, Ogg, Opus, Ogg FLAC, WAV or AIFF file keeps the DSP.
				A DSP change while paused keeps the position, and the SqueezeDSP page shows no figures for a stream
				the DSP was switched off for.
	0.1.72.9	Fork: PCM mode's WAV header states the track's length (the length still to play plus a second) instead of "unknown",
				so a renderer that reads its length from the header (the Platin Hub) shows it.
	0.1.72.8	Fork: the SqueezeDSP page no longer says SqueezeDSP is out of the signal path near the end of each track.
				Once the whole track has been sent to the player it keeps that track's final figures and says so, and
				when the next track's processing starts early it names that track and shows its source.
	0.1.72.7	Fork: a new SqueezeDSP page (/squeezedsp), opened from Material's Home screen or any player's menu: the
				player's signal path as it runs, with its processing speed (how many times faster than realtime the
				slowest stage works, from the processes LMS runs; Linux servers), and the main controls: DSP, output,
				Upsample with its phase and filter options, preamp, balance, width and presets. The equaliser, filters and
				the rest stay on the full settings page, linked from the new one.
	0.1.72.6	Fork: Upsample phase on the settings page: linear (as before, ffmpeg's soxr), intermediate or minimum (SoX's
				resampler, with 6 dB taken off before it and put back after, so every phase plays at the same level). The
				page shows the precision and passband the player really has after a refresh (it showed the first option).
	0.1.72.5	Fork: with Upsample on, streamed tracks (Qobuz and the like) and resumes start at once again. ffmpeg held back its
				output until it had read about 5 s of the track, and LMS feeds a stream only as output comes back.
	0.1.72.4	Fork: Upsample (per player, default off): every track but DSD goes to the player's top rate in its own family
				(44.1 or 48 kHz) before the DSP, by ffmpeg's SoX resampler (soxr) in floating point, so inter-sample peaks reach
				the DSP as real samples that its peaks report and Preamp can give headroom. Precision, passband and a steep
				(Chebyshev) filter on the settings page. Needs the ffmpeg LMS uses to have soxr; without it the page says why.
	0.1.72.3	Fork: local tracks start at once again (gapless): every type that can read a file has its own file rule, so the
				decoder reads the file instead of LMS feeding it slowly on stdin. DSD, BBC Sounds, internet radio, Ogg, Opus, WMA
				and HLS play again: their decoders hand the engine raw PCM, because a WAV header of unknown length on a pipe made
				the engine print a warning into its audio output.
	0.1.72.2	Fork: pause and seek on a FLAC stream work again. FLAC and Ogg FLAC are back on one raw rule for files and streams,
				as upstream: a resumed stream reaches the rule with no STREAMINFO, so a WAV header could not describe it.
	0.1.72.1	Fork (SimonArnold002): DSP defeatable per player (a DSP-off player plays as if the plugin were absent, LMS's own rules are never
				deleted), every input type through the DSP, 24-bit FLAC out or PCM as 24-bit WAV for players that read a WAV header,
				decoders hand the engine a WAV header, DSD at the player's DXD target, no DSP below FLAC, toggles apply without a restart.
	0.1.72	Fox: Added support for SpotOn plugin, removing conversion leaves non-competing fmt -> fmt conversion alone.
	0.1.71	Fox: Updates to core binary outputs mp3 and flac and also transcoding now simpler but requires different cleanup
	0.1.61	Fox: Force fixing custom-conf.conf re-write and amending headphone dropdown to scale better
	0.1.60  Fox: Added replay gain, fir filter strength, toggles for panels
	0.1.51	Fox: Proper empty structure created and returned for new player, error was causing issues on UI
	0.1.50  Fox: Added a clear log function and added HLS to the fully supported list of formats for transcoding, this should allow support for streaming services that use HLS such as Apple Music and Amazon Music.
    0.1.41  Fox: css changes to fix minor issues with the UI
    0.1.40	Fox: Adding a visualiser for PEQ and Loudness Settings
    0.1.34	Fox: Fix to loading presets, should apply them. Styling change to tooltips.
    0.1.33	Fox: Revised Binary, new residual cache with expiry time for delay, and convolver leftovers -   
              Fixed issues with saving and loading presets not retaining preset names
    0.1.32	Fox: Revised Binary, fixed gapless playback by caching latest player filter & fft, 
                Fixed gain drop fixed moved tail saver to eradicate race condition. 
                Tweaked preset load logic so that clientname, ID and Last-Preset are corrected.
                Amended dynamic application of amended settings so that it will only work if a track is playing.
    0.1.31	Fox: Revised Binary, fixed gapless playback by caching latest player filter and fixed issue with 24 bit aiff file not being read correctly
	0.1.30	Fox: Revised behaviour so that player settings are created on the UI and saved here rather than managed here. This simlifies design and communication between the UI and the binary.

	0.1.28	Fox: Found issue where there is only a PEQ filter, then the checks for FIR filter cause early termination.
	0.1.27	Fox: Found issue where there is no filter of any kind, end of processing tail handler fails and closes prematurely.
	0.1.26	Fox: Found issue where there is no filter of any kind. Output runs but with zero gain.
	0.1.25	Fox: Binary using sox for resampling, now that I have figured out the highest quality settings. Normaliser is based off FFT peak, as more accurate - code cleaned
				Transcoder, fixing issue with alc where the parameter order affects whether transcoding works. 
	0.1.24	Fox: Fixed issue with cleanup vs REW filters being wrong, now detects where impulse peak is at start of the file and trims accordingly
				Extended length of PEQ filter, likely inaudible but should be more effective
				Amended mp3 transcoder so that it works with talkSport. (lame now outputs wav rather than raw)
	0.1.23	Fox: Changes to binary, Bug fixes - Disable Low/High Pass amd :ow/High Shelf filters no longer build filters
				Loudness filter working
				width filter gain neutral, before gain was super-boosted
				FIR Filters are now trimmed to the point where level is -70 dBFS with a margin and windowing.
				Fixed Minor issue with convolver when merging fir and PEQ filters
				This seems to fix the Housecurve filter issue and results in smaller filters that are just as effective

	0.1.22	Fox: Major bug fixes to binary, including:
				Wav Reader now handles PCM and AIF correctly as well as detecting end of data section for WAV files, this stope early termination
				Fixed issue with Wav Reader, reading only frame samples rather than the intended block loading of 1/5 second this fixed issue with stuttering
				Fixed issue with Convolver setting signal length = impulse length. This could cause choking on large impulses as buffers were emptied
				Reduced buffer sizes for GO Channel processing as no longer needed, reduces memory footprint
				
	0.1.21	Fox: Performance improvements to binary following profile runs test in 60% of time
	0.1.20	Fox: New Binary - using golang
	0.1.11	Fox: New Binary experimental sopport for MacOS arm 64 native
	0.1.10	Fox: Initialises log file, added better error handling where it is not created as this was causing failures, added better error handling for JSON reads that were failing
	0.1.09	Fox: Cleaned up code for updating the preset for a player, so that it won't keep updating for Smart TVs that run polling processes. Added cleanup for preset files deleted bands.
	0.1.08	Fox: Amended templates for Spotty and prettified the JSON settings at last
	0.1.07	Fox: Amended mechanism for deriving the settings folder by passing it from the plugin script to the binary via the config file. This should enable MacOs install to work
	0.1.06	Fox: Revised Binary, impulse loaded and resampled via SoX with no temp file used, SoX resampler was more accurate than new internal one.
				Convolver code for Impulses revised to use Externl FFT.Calculation as this seems more accurate
				A number of code tweaks, suggested for improving speed. Mainly optimising loops.
				Corrected an issue with Low Pass filter, which generated rubbish when approaching Nyquist frequency.
				Added Loudness fine-tuning and Width processing, and this needed amendments to UI and Perl Script	
	0.1.05	Fox: New Binary no longer using SoX wrapped but internal library instead. Amending this code to automatically update binaries
	0.1.04	Fox: Code added in to remove native conversion and competing flac conversions so that SqueezeDSP is the default
				 this code was reworked from C3P0 plugin.
	0.1.01	Fox: Added in logging interface, new call will read SqueezeDSP log file.
	0.1.00	Fox: Binary update PLugin update to version number
	0.0.98	Fox: New EQ Bands now appearing in correct place, wth no weird defaults
			New players now have default JSON files created properly
	0.0.97	Fox: Adding and deleting a frequency band working predictably off a simple default
	0.0.95	Fox: Commented out references to CamillaDSP, using sox instead. Added fallback default for EQ frequency
	0.0.92	Fox: Fixed Case sensitive filenames on Linux
	0.0.91	Fox: Cleaning up for installation
	0.0.11	Fox: removing Debug settings - ready for release
	0.0.7 - Fox:
			Using JSON files for config - that is app config and for the DSP/EQ settings.
			fixed housekeeping issue
	0.0.5 - Fox:
			This is a fully working initial version
			All code has been migrated to refer to SqueezeDSP
			code relating to the signal generator has been removed.
			code relating to the jive menu has been removed and moved into a separate pm file, which is not referenced or used.
			At this point, I think it is unlikely that the Jive menus will be re-instated
=cut
use strict;
use base qw(Slim::Plugin::Base);
use Slim::Utils::Misc;
use Slim::Utils::OSDetect;
use File::Spec::Functions qw(:ALL);
use FindBin qw($Bin);
use Slim::Utils::Log;
use Slim::Utils::Prefs;
#test
use POSIX qw(strftime);  # add this
use Data::Dumper;
use Time::HiRes qw(time);

# ======== START: VARIABLE DECLARATIONS ========
# Package-level declarations (at the top, before any subs)
our (
    $log,                # Log object
    $prefs,              # Preferences object
    $thisapp,            # Application name
    $pluginDataDir,      # Main plugin directory
    $pluginSettingsDataDir, # Settings directory
    $pluginImpulsesDataDir, # Impulses directory
    $pluginMatrixDataDir,   # Matrix directory
    $pluginTempDataDir,     # Temp directory
    $convolver,          # Convolver name
    $logfile,            # Log file path
    $fatalError,         # Fatal error message
    $needUpgrade,        # Upgrade flag
    $doneJiveInit,       # Jive init flag
    $configPath,         # Config path
    $thistag,            # Plugin tag
    $myconfigrevision,   # Config revision
    $binversion,          # Binary version
	$cacheBust,          # Cache busting token
	$gainLog          # gain logger
);


# Revision number
my $revision = "0.1.72.25";
$binversion = "0_2_40";
use vars qw($VERSION);
$VERSION = $revision;

# Names and name-related constants...
$thistag = "squeezedsp";
$thisapp = "SqueezeDSP";
my $binary;
my $settingstag = "SqueezeDSPSettings";
# package variables: Configuration.pm writes and parses the conf markers with these
our $confBegin = "SqueezeDSP#begin";
our $confEnd = "SqueezeDSP#end";
my $modeAdjust       = "PLUGIN.SqueezeDSP.Adjust";
my $modeValue        = "PLUGIN.SqueezeDSP.Value";
my $modePresets      = "PLUGIN.SqueezeDSP.Presets";
my $modeSettings     = "PLUGIN.SqueezeDSP.Settings";
my $modeEqualization = "PLUGIN.SqueezeDSP.Equalization";
my $modeRoomCorr     = "PLUGIN.SqueezeDSP.RoomCorrection";
my $modeMatrix       = "PLUGIN.SqueezeDSP.Matrix";

our %noFunctions = ();
#$needUpgrade = 0;
$fatalError = undef;
$doneJiveInit = 0;
# ======== END: VARIABLE DECLARATIONS ========

# Load submodules
use Plugins::SqueezeDSP::Settings;
use Plugins::SqueezeDSP::TemplateConfig;
use Plugins::SqueezeDSP::Configuration;
use Plugins::SqueezeDSP::UI_Functions;
use Plugins::SqueezeDSP::Utils;
use Plugins::SqueezeDSP::Binary;
use Plugins::SqueezeDSP::Upsample;
use Plugins::SqueezeDSP::WavLength;
use Plugins::SqueezeDSP::Status;
use Plugins::SqueezeDSP::Page;

sub initPlugin {
    my $class = shift;
    $class->SUPER::initPlugin( @_ );

    # ======== START: VARIABLE INITIALIZATION ========
    # Initialize log and preferences
    $log = Slim::Utils::Log->addLogCategory({ 
        'category' => 'plugin.' . $thistag, 
        'defaultLevel' => 'WARN', 
        'description'  => $thisapp 
    });
    $prefs = preferences('plugin.' . $thistag);
    
    # Initialize directories
    my $appdata = Slim::Utils::Prefs::dir();
    $pluginDataDir = catdir($appdata, $thisapp);
    mkdir($pluginDataDir);
    $pluginSettingsDataDir = catdir($pluginDataDir, 'Settings');
    mkdir($pluginSettingsDataDir);
    $pluginImpulsesDataDir = catdir($pluginDataDir, 'Impulses');
    mkdir($pluginImpulsesDataDir);
    $pluginMatrixDataDir = catdir($pluginDataDir, 'MatrixImpulses');
    mkdir($pluginMatrixDataDir);
    $pluginTempDataDir = catdir($pluginDataDir, 'Temp');
    mkdir($pluginTempDataDir);
    
    # Other initializations
    $convolver = "SqueezeDSP";
    $logfile = catdir(Slim::Utils::OSDetect::dirsFor('log'), "squeezedsp.log");

	#$gainLog = catdir(Slim::Utils::OSDetect::dirsFor('log'), "gain.log");  # add this
	
    # a template revision bump forces a conf rewrite only when this is set
    $myconfigrevision = Plugins::SqueezeDSP::TemplateConfig::get_config_revision();
    # ======== END: VARIABLE INITIALIZATION ========

    Plugins::SqueezeDSP::Utils::debug("plugin " . $revision . " enabled");

    # Upsample picks each track's target at stream start through a wrap of LMS's command
    # builder (Upsample.pm), so it goes in before any player's rules are written. Without it,
    # or without an ffmpeg that has soxr, no player gets upsample rules.
    if ( Plugins::SqueezeDSP::Upsample::install() ) {
        my $ff = Plugins::SqueezeDSP::Upsample::ffmpegPath();
        my $msg = $ff ? ( Plugins::SqueezeDSP::Upsample::soxr() ? "ffmpeg $ff has soxr: upsampling available"
                                                               : "ffmpeg $ff has no soxr: upsampling unavailable" )
                      : "no ffmpeg found: upsampling unavailable";
        Plugins::SqueezeDSP::Utils::debug($msg);
        my $perl = Plugins::SqueezeDSP::WavLength::perl();
        Plugins::SqueezeDSP::Utils::debug( "PCM mode states each track's length in its WAV header (the engine's --wavlength; "
            . ( $perl ? "a filter on $perl after a resample to the player's max)" : "after a resample to the player's max it says unknown: no perl beside LMS)" ) );
    } else {
        $log->warn("LMS has no tokenizeConvertCommand2 to wrap: upsampling unavailable");
    }

    # Register json/CLI functions
    Slim::Control::Request::addDispatch([$thistag . '.filters'], [1, 1, 0, \&Plugins::SqueezeDSP::UI_Functions::filtersQuery]);
    Slim::Control::Request::addDispatch([$thistag . '.saveas'], [1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::saveasCommand]);
    Slim::Control::Request::addDispatch([$thistag . '.logsummary'], [1, 1, 0, \&Plugins::SqueezeDSP::UI_Functions::logsummaryQuery]);
    Slim::Control::Request::addDispatch([$thistag . '.saveall'], [1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::saveallCommand]);
    Slim::Control::Request::addDispatch([$thistag . '.readclientSettings'], [1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::readClientSettings]);
    Slim::Control::Request::addDispatch([$thistag . '.readpresetSettings'], [1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::readPresetSettings]);
    Slim::Control::Request::addDispatch([$thistag . '.importwav'], [1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::importwavCommand]);
	Slim::Control::Request::addDispatch([ $thistag . '.deletepreset' ], [ 1, 1, 1, \&Plugins::SqueezeDSP::UI_Functions::deletepresetCommand ]);
    Slim::Control::Request::addDispatch([$thistag . '.clearlog'], [1, 1, 0, \&Plugins::SqueezeDSP::UI_Functions::clearlogCommand]);

	Slim::Control::Request::addDispatch([$thistag . '.trackgain'], [1, 1, 0, \&Plugins::SqueezeDSP::Utils::_trackGainQuery]);
    # the SqueezeDSP page's signal path and processing speed (Status.pm), polled while it is open
    Slim::Control::Request::addDispatch([$thistag . '.status'], [1, 1, 0, \&Plugins::SqueezeDSP::Status::statusQuery]);



    # Binary setup and housekeeping
    Plugins::SqueezeDSP::Binary::setup_binary($class);
    Plugins::SqueezeDSP::Binary::housekeeping();

    # LMS's native rules are left alone: a player with DSP off must play exactly as if
    # this plugin were not installed. Our rules win per player through the model slot
    # and the player's format list instead (Configuration::applyFormatOrder), which
    # also leaves the server's prioritizeNative nothing to move ahead of them.

    # Event subscription
    #Slim::Control::Request::subscribe(\&clientEvent, [['client'],['new', 'reconnect']]);
	Slim::Control::Request::subscribe(\&clientEventNew, [['client'],['new']]);
	Slim::Control::Request::subscribe(\&clientEventReconnect, [['client'],['reconnect']]);
	Slim::Control::Request::subscribe(\&syncEvent, [['sync']]);
	# a player's Bitrate Limiting (LMS's player settings, Audio) decides whether its stream is MP3 at that limit
	# (Configuration::reducedOutput): its rules and list follow a change at once. A client pref's change handler
	# gets the client third (Prefs/Base.pm set, Prefs/Client.pm _obj).
	preferences('server')->setChange( \&bitrateChange, 'maxBitrate' );

	# add this for gain logging
	#Slim::Control::Request::subscribe(\&_trackChanged, [['playlist'],['newsong']]);

	# will clear cache on server start, so changing js files works
	$cacheBust = time();

}

# The SqueezeDSP page in Material: ONE entry, in the 'allplayers' section, opening the page inline
# (iframe; a weblink tears off a separate window) for the player it was picked from. Material
# shows an 'allplayers' action in each player's menu (Manage players, player settings), in the
# main menu's player list, and as a Home tile, with $ID replaced by that player's id or the
# current one (customactions.js getCustomActions and doReplacements, the same in 6.4.9 to 6.4.12,
# run in JavaScriptCore 2026-10-24). So 'pinned' is not registered as well: that would be a
# second Home tile. registerCustomAction pushes and never de-dupes, so this runs once per server
# run, and it is reached through ->can, so a server without Material simply has no entry.
my $materialDone;
sub postinitPlugin {
    return if $materialDone;
    my $register = eval { Plugins::MaterialSkin::Plugin->can('registerCustomAction') };
    return unless $register;
    $materialDone = 1;
    my $ok = eval {
        $register->( 'allplayers', {
            title  => Slim::Utils::Strings::string('PLUGIN_SQUEEZEDSP_DISPLAYNAME'),
            iframe => Plugins::SqueezeDSP::Page::PATH() . '?player=$ID',
            icon   => 'equalizer',
        } );
        1;
    };
    $log->warn("could not register the SqueezeDSP page with Material: $@") unless $ok;
}

sub shutdown {
    Slim::Control::Request::unsubscribe(\&clientEvent);
	#gain logging
	Slim::Control::Request::unsubscribe(\&_trackChanged);  # add this
}



sub clientEventNew {
    my $request = shift;
    my $client  = $request->client();
    return unless defined $client;
	Plugins::SqueezeDSP::Utils::debug( "New Client detected" );
    Plugins::SqueezeDSP::Configuration::applyClientState($client);
}

sub clientEventReconnect {
    my $request = shift;
    my $client  = $request->client();
    return unless defined $client;
	Plugins::SqueezeDSP::Utils::debug( "Existing Client reconnected" );
    # a HELO resets the player's format list, so the order is applied again
    Plugins::SqueezeDSP::Configuration::applyClientState($client);
}

# LMS's maxBitrate pref changed for a player: ( pref, new value, client, old value )
sub bitrateChange {
    my ( undef, undef, $client ) = @_;
    return unless ref $client && eval { $client->can('id') };
    Plugins::SqueezeDSP::Utils::debug( "Bitrate Limiting changed for " . $client->name );
    Plugins::SqueezeDSP::Configuration::applyClientState($client);
}

sub syncEvent {
    my $request = shift;
    my $client  = $request->client();
    return unless defined $client;
    Plugins::SqueezeDSP::Configuration::warnMixedSync($client);
}

sub webPages {
    my $class = shift;
    if (Slim::Utils::PluginManager->isEnabled("Plugins::SqueezeDSP::Plugin")) {
        Slim::Web::Pages->addPageFunction("plugins/SqueezeDSP/index.html", \&handleWebIndex);
        Slim::Web::Pages->addPageLinks("plugins", { $class->getDisplayName => 'plugins/SqueezeDSP/index.html' });
        # the SqueezeDSP page, a raw handler at /squeezedsp (Page.pm); its version handed in here
        Plugins::SqueezeDSP::Page->init($revision);
    } else {
        Slim::Web::Pages->addPageLinks("plugins", { $class->getDisplayName => undef });
        Slim::Web::Pages->addPageLinks("icons", { $class->getDisplayName => undef });
    }
}


sub handleWebIndex {
    my ($client, $params) = @_;
    $params->{cacheBust} = $cacheBust;
    return Slim::Web::HTTP::filltemplatefile('plugins/SqueezeDSP/index.html', $params) if $client;
}


sub getFunctions { return \%noFunctions; }
sub getDisplayName { return 'PLUGIN_SQUEEZEDSP_DISPLAYNAME'; }
sub enabled { return 1; }
sub getpluginVersion { return $revision; }



1;