package Plugins::SqueezeDSP::Configuration;
use strict;
use File::Spec::Functions qw(:ALL);
use Slim::Player::TranscodingHelper;
use Plugins::SqueezeDSP::Utils;
use Plugins::SqueezeDSP::TemplateConfig;
use Plugins::SqueezeDSP::Upsample;

# the state of a client whose section holds markers only: DSP off, or not connected
use constant STATE_OFF => 'dsp=off';

my %_initInProgress = ();
my $_conversionTablesClean = 0;
# each rewrite's own temporary file (initConfiguration)
my $_confWrites = 0;

# ------ configuration helpers ------
sub initConfiguration {
    my $client = shift;
	#my $clientId = $client->id();
    Plugins::SqueezeDSP::Utils::debug( "client " . $client->id() ." name " . $client->name ." initializing" );

 	#return if $_initInProgress{$clientId};
    #$_initInProgress{$clientId} = 1;

	$Plugins::SqueezeDSP::Plugin::fatalError = undef;
    my $upgradeReason = '';
    my @origLines;
    my @clientIDs = sort map { $_->id() } Slim::Player::Client::clients();
    my @foundClients;
    my %markedState;
    my $marker = _markerRegex();

    @origLines = ();
    $Plugins::SqueezeDSP::Plugin::configPath = Plugins::SqueezeDSP::Utils::newConfigPath();
    Plugins::SqueezeDSP::Utils::debug ( "Config Path: " . $Plugins::SqueezeDSP::Plugin::configPath);

    if( -f $Plugins::SqueezeDSP::Plugin::configPath ) {
        open( my $config, '<', $Plugins::SqueezeDSP::Plugin::configPath ) || do {
            Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Can't read from $Plugins::SqueezeDSP::Plugin::configPath" );
            return 0;
        };
        @origLines = <$config>;
        close( $config );

        # Check for corruption: empty file, no begin marker, or a begin marker not in
        # today's form (upstream's read '# ##rev:...', the state field is new)
        my @beginLines = grep( /#rev:.*#client:/i, @origLines );
        my $badMarkers = grep( $_ !~ $marker, @beginLines );
        if( !@origLines || !@beginLines || $badMarkers ) {
            Plugins::SqueezeDSP::Utils::debug( "Config file missing or corrupt, will rebuild fresh" );
            $upgradeReason = "Config file missing or corrupt" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
            $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
            @foundClients = @clientIDs;   # Include all current clients
            @origLines = ();              # Discard old contents
        } else {
            my @revs = split /\./, $Plugins::SqueezeDSP::Plugin::myconfigrevision;
            for( @beginLines ) {
                if( my ( $rev, $markedClient, $state ) = $_ =~ $marker ) {
                    my @test = split /\./, $rev;
                    for my $ver ( @revs ) {
                        my $vert = shift( @test ) || 0;
                        next if $vert == $ver;
                        Plugins::SqueezeDSP::Utils::debug ("Template version higher than config: " . $ver . " - " . $vert );
                        $upgradeReason = "Previous version $rev less than my $Plugins::SqueezeDSP::Plugin::myconfigrevision ($vert less than $ver)" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
                        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                        last;
                    }
                    push( @foundClients, $markedClient );
                    $markedState{ lc $markedClient } = $state;
                }
            }
            unless( $Plugins::SqueezeDSP::Plugin::needUpgrade ) {
                for my $c (@clientIDs) {
                    my $ok = 0;
                    for my $cc (@foundClients) {
                        if( $c eq $cc ) {
                            $ok = 1;
                            last;
                        }
                    }
                    unless( $ok ) {
                        $upgradeReason = "Client $c was not yet registered" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
                        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                        Plugins::SqueezeDSP::Utils::debug ("Client $c was not yet registered" );
                        push( @foundClients, $c );
                        last;
                    }
                }
            }
            # A known client rewrites too when its state moved: DSP switched, another
            # output or model, or it was offline (markers only) when the file was written.
            unless( $Plugins::SqueezeDSP::Plugin::needUpgrade ) {
                for my $c (@clientIDs) {
                    my $state = clientState( Slim::Player::Client::getClient($c) );
                    my $was = $markedState{ lc $c };
                    next if defined $was && $was eq $state;
                    $upgradeReason = "Client $c state " . ( $was // 'none' ) . " -> $state";
                    $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
                    Plugins::SqueezeDSP::Utils::debug ( $upgradeReason );
                    last;
                }
            }
        }
    } else {
        Plugins::SqueezeDSP::Utils::debug ("Config Not found, new one created" );
        $upgradeReason = "New configuration" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
        @foundClients = @clientIDs;
    }

    # One section per client. On plex, after an install, the conf came to hold Lounge's and the iPhone's twice (ledger
    # DUPLICATE CONF BLOCKS; cause not found), so LMS held their rules twice. A client listed again is written once,
    # and the rewrite that does it says so at WARN with the list it read, so a recurrence shows its cause.
    my %seen;
    my @twice = grep { $seen{ lc $_ }++ == 1 } @foundClients;
    if ( @twice ) {
        $Plugins::SqueezeDSP::Plugin::log->warn( "$Plugins::SqueezeDSP::Plugin::thisapp: the conf lists @twice more than once,"
            . " so each is written once (read: @foundClients; rewriting for " . $client->id() . ")" );
        $upgradeReason = "Client @twice held twice" unless $Plugins::SqueezeDSP::Plugin::needUpgrade;
        $Plugins::SqueezeDSP::Plugin::needUpgrade = 1;
        my %once;
        @foundClients = grep { !$once{ lc $_ }++ } @foundClients;
    }

    upgradePrefs( @foundClients );
    return 1 unless $Plugins::SqueezeDSP::Plugin::needUpgrade;

    Plugins::SqueezeDSP::Utils::debug( "Need to rewrite " . $Plugins::SqueezeDSP::Plugin::configPath . " (" .$upgradeReason . ")" );
    # written whole to a file of this rewrite's own, then renamed over the conf, so nothing reading the conf (LMS, the
    # next rewrite) ever finds half of one
    my $path = $Plugins::SqueezeDSP::Plugin::configPath;
    my $tmp  = "$path.$$." . ++$_confWrites;
    open( my $fh, '>', $tmp ) || do {
        Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Can't write to $tmp" );
        return;
    };

    my $now = localtime;
    print $fh "# Modified by $Plugins::SqueezeDSP::Plugin::thisapp, $now: $upgradeReason\n";
    foreach ( @origLines ) {
        print $fh $_ if m/# Modified by $Plugins::SqueezeDSP::Plugin::thisapp/i;
    }
    print $fh "\n";

	foreach my $clientID ( @foundClients ) {
		# A client that is not connected has no settings or model to read: markers only.
		my $client = Slim::Player::Client::getClient($clientID);
		my $state = clientState($client);

		print $fh "# #$Plugins::SqueezeDSP::Plugin::confBegin#rev:$Plugins::SqueezeDSP::Plugin::myconfigrevision#client:$clientID#state:$state# ***** BEGIN AUTOMATICALLY GENERATED SECTION - DO NOT EDIT ****\n";

		# DSP off: no rules at all, so LMS treats the player as if the plugin were absent
		if ( $state ne STATE_OFF ) {
			# 24-bit FLAC out; WAV mode adds the WAV rules ahead of it. A reduced stream has its own rules
			# alone (the player's list leads with that format alone: applyFormatOrder).
			my $flac = ( $state =~ /\bpath=engine\b/ ) ? 'FLACENGINE' : 'FLAC24';
			my ($out) = $state =~ /\bout=(\w+)/;
			my @formats = isReduced($out) ? ( $out ) : ( $out eq 'WAV' ) ? ( 'WAV', $flac ) : ( $flac );
			# the same settings the state was taken from, the targets for each template's own output: a PCM-mode
			# player's FLAC rules stop at FLAC's cap, as LMS's flac 1.3.4 refuses 705.6k and 768k
			my $prefs = clientPrefs($client);
			my $on = $state =~ /\bup=on\b/;

			# Generate and print a template for each format
			foreach my $fmt (@formats) {
				my $up = ( $on && !isReduced($fmt) ) ? upsampleSpec( $client, $prefs, $fmt eq 'WAV' ? 'WAV' : 'FLAC' ) : undef;
				my $t = template($clientID, $fmt, modelSlot($client), $up);
				print $fh $t;
				print $fh "\n";
			}
		}

		print $fh "# #$Plugins::SqueezeDSP::Plugin::confEnd#client:$clientID# ***** END AUTOMATICALLY GENERATED SECTION - DO NOT EDIT *****\n";
		print $fh "\n";
	}

    # Verify that the file was written successfully and is non‑empty, then put it in place
    if( !close( $fh ) || !-s $tmp ) {
        Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Config file written but appears empty!" );
        unlink $tmp;
        return;
    }
    rename( $tmp, $path ) || do {
        Plugins::SqueezeDSP::Utils::fatal( $client, undef, "Can't replace $path: $!" );
        unlink $tmp;
        return;
    };

    # Optionally set permissions so the file can be manually edited
    chmod 0666, $path;   # or 0644 for owner‑only write

    $Plugins::SqueezeDSP::Plugin::needUpgrade = 0;
    syncCommandTable( $Plugins::SqueezeDSP::Plugin::configPath );
    return 1;
}

# The begin marker of a client's section, with the state it was written for:
#   # #SqueezeDSP#begin#rev:0.3.00#client:aa:bb:cc:dd:ee:ff#state:dsp=on,out=FLAC,...# ***** BEGIN ...
# Strict, so upstream's markers (and its greedy '#client:(.*)#') read as corrupt.
sub _markerRegex {
    my $begin = quotemeta( $Plugins::SqueezeDSP::Plugin::confBegin );
    return qr/#$begin#rev:([0-9.]+)#client:([^#\s]+)#state:([\w=,.+*-]+)#/i;
}

# The state a connected client's section must be written for, as its marker records
# it. Anything but STATE_OFF writes rules; DSP is on only when Bypass is exactly 0.
sub clientState {
    my $client = shift;
    return STATE_OFF unless defined $client;

    # No DSP below FLAC: such a player always plays as if the plugin were absent
    return STATE_OFF unless playerTakes( $client, 'dsp' );

    my $prefs = clientPrefs($client);
    return STATE_OFF unless defined $prefs->{Bypass} && $prefs->{Bypass} eq '0';

    my $out = outputFormat( $client, $prefs );
    # Upsample records its targets and filter options too, so a change to any of them rewrites the rules; a
    # reduced stream is never upsampled (upsampleSpec)
    my $spec = upsampleSpec( $client, $prefs );
    my @up = $spec ? ( 'up=on', "ut=$spec->{f44}.$spec->{f48}", "uq=$spec->{precision}", "us=$spec->{steep}", "ub=$spec->{passband}", "uph=$spec->{phase}" ) : ( 'up=off' );
    # MP3 at a Bitrate Limiting records the limit, so a new limit rewrites the rules
    my $kbps = mp3Limit( $client, $out );
    push @up, "br=$kbps" if $kbps;

    # DSD's decoder and resampler (Upsample::dsdFilter) depend on the server's ffmpeg: after the restart a new
    # ffmpeg needs, one with soxr, one without it or none (DSDPlayer's dsdplay: dxr=dsdplay, else LMS's sox: dxr=sox)
    # rewrites the rules
    my @state = ( 'dsp=on', "out=$out", @up, 'model=' . modelSlot($client), 'dxd=' . dxdTarget( $client, $spec, $out ),
                  'dxr=' . dsdTool() );
    # the static A/B diagnostic (no UI): the engine encodes FLAC itself, as 0.1.71 did
    push @state, 'path=engine' if defined $prefs->{OutputPath} && $prefs->{OutputPath} eq 'engine';
    return join( ',', @state );
}

# What decodes DSD and takes it to its target: ffmpeg through soxr ('soxr') or its own resampler ('swr'); with no
# ffmpeg on the server DSDPlayer's dsdplay where that plugin is enabled ('dsdplay': TemplateConfig _dsdPlay, which
# seeks at once), else LMS's own sox ('sox': TemplateConfig _dsdSox, whose seek decodes up to its point)
sub dsdTool {
    return 'soxr' if Plugins::SqueezeDSP::Upsample::soxr();
    return 'swr'  if Plugins::SqueezeDSP::Upsample::ffmpegPath();
    return encoderPath('dsdplay') ? 'dsdplay' : 'sox';
}

# The player's settings ({} when it has none yet)
sub clientPrefs {
    my $client = shift;
    my $settings = Plugins::SqueezeDSP::Utils::LoadJSONFile( Plugins::SqueezeDSP::Utils::getPrefFile($client) ) || {};
    return ref $settings->{Client} eq 'HASH' ? $settings->{Client} : {};
}

# The player's output: a reduced stream ('MP3', 'OPS', 'OGG': reducedOutput) wherever one is due; else PCM
# mode is 24-bit WAV with its header, for a player that reads one ('WAV'); any other player stays on FLAC
sub outputFormat {
    my ( $client, $prefs ) = @_;
    my $reduced = reducedOutput($client);
    return $reduced if $reduced;
    return ( defined $prefs->{OutputFormat} && $prefs->{OutputFormat} eq 'PCM' && playerTakes( $client, 'wav' ) ) ? 'WAV' : 'FLAC';
}

# ------ reduced streams ------
# Simon 2026-11-01: "bit rate limiting is to reduce down bandwidth to players if they are on slower network speeds
# or poor wifi", "It should be reducing our output to mp3 not sending mp3 to be upscaled", "we would never upsample
# to then downgrade to bit rate limiting but DSP may still be required. Same for other apps that now request to
# convert to opus for cellular playback". A reduced stream keeps the DSP, at the track's own rate, and is encoded
# after it (TemplateConfig _reduced):
#   - a player with Bitrate Limiting set gets MP3 at that limit, as LMS's own rules give it;
#   - an app that could play 24-bit FLAC but leads its list with a lossy format (LyrPlay's Opus, Ogg, AAC and
#     MP3 modes: _lossyApp) gets the first one of its list we can encode, at Mobile Transcode Rules' qualities:
#     Opus (opusenc), Ogg Vorbis (LMS's sox) or MP3 (lame; AAC falls to it, as Mobile Transcode does).
# The format an app lists, by the reduced output that serves it
my %LOSSY = ( ops => 'OPS', ogg => 'OGG', mp3 => 'MP3', aac => 'MP3' );
# the encoder each reduced output needs, and the format it streams as
my %ENCODER = ( MP3 => 'lame', OPS => 'opusenc', OGG => 'sox' );
my %STREAM  = ( MP3 => 'mp3', OPS => 'ops', OGG => 'ogg' );

sub isReduced { return ( defined $_[0] && $ENCODER{ $_[0] } ) ? 1 : 0 }

# The format a reduced output streams as ('mp3', 'ops', 'ogg'), or undef
sub reducedStream { return defined $_[0] ? $STREAM{ $_[0] } : undef }

# An encoder, or DSDPlayer's dsdplay, as LMS's [name] finds it (findbin: LMS's own Bin folders and every enabled
# plugin's, then the PATH), or ''. Cached for the server's life: a newly installed one needs a restart, as LMS's own
# binary cache does (TranscodingHelper %binaries), and so does enabling or disabling a plugin.
our %ENCODERS;
sub encoderPath {
    my $name = shift;
    return $ENCODERS{$name} //= ( eval { Slim::Utils::Misc::findbin($name) } || '' );
}

# The player's Bitrate Limiting in kbps, 0 for none: LMS's own reading of it for this player alone, the default
# included (Prefs.pm maxRate: no limit for the SB2's line, squeezelite and SqueezePlay among it). Sync groups are
# not supported (ledger SYNC GROUPS NOT SUPPORTED), so a group's lower limit is not looked for.
sub bitrateLimit {
    my $client = shift;
    my $r = eval { Slim::Utils::Prefs::maxRate( $client, 1 ) } || 0;
    return ( $r =~ /^\d+$/ ) ? 0 + $r : 0;
}

# The limit an MP3 output is encoded at (kbps), or 0 for an app's MP3 at its fixed quality
sub mp3Limit {
    my ( $client, $out ) = @_;
    return 0 unless defined $out && $out eq 'MP3';
    return bitrateLimit($client);
}

# The reduced output due to a player that takes the DSP, or undef. Only for a player whose list we set (myFormats):
# its list then leads with that format alone. A limit wins, where the server has lame and the player lists mp3;
# without lame the limit cannot be honoured, so the player keeps what it would have without one.
sub reducedOutput {
    my $client = shift;
    return undef unless defined $client && $client->can('myFormats') && ref $client->myFormats eq 'ARRAY';
    my @list = nativeFormats($client);
    my %has = map { $_ => 1 } @list;
    return 'MP3' if $has{mp3} && bitrateLimit($client) && encoderPath('lame') && _canDsp( $client, \@list );
    return _lossyApp( $client, \@list );
}

# Whether a player can take the DSP: 24-bit FLAC (in LMS 9.1 that leaves out SliMP3 and the Squeezebox 1;
# SoftSqueeze is left out by name, as upstream did), or an app asking for a reduced stream (_lossyApp)
sub _canDsp {
    my ( $client, $list ) = @_;
    return 1 if ( grep { $_ eq 'flc' } @$list ) && ( $client->model() // '' ) ne 'softsqueeze';
    return _lossyApp( $client, $list ) ? 1 : 0;
}

# A player whose list we set (myFormats), whose own list has no flc and leads with a lossy format: an app asking
# for a smaller stream. In LMS 9.1 only SqueezePlay's class keeps a list that can be set (SqueezePlay.pm:33): the
# class of squeezelite, SqueezePlay and LyrPlay, under Squeezebox2 (:26), so all able to play 24-bit FLAC; SliMP3,
# the Squeezebox 1 and SoftSqueeze have fixed lists, so the line drawn at 24-bit FLAC stands. Its reduced output,
# the first format of its list we can encode, or undef. A list leading with anything else (LyrPlay's WAV mode, wav
# first) is not a request for a smaller stream.
sub _lossyApp {
    my ( $client, $list ) = @_;
    return undef unless $client->can('myFormats') && ref $client->myFormats eq 'ARRAY';
    return undef unless @$list && $LOSSY{ $list->[0] } && !grep { $_ eq 'flc' } @$list;
    for my $f (@$list) {
        my $out = $LOSSY{$f} or next;
        return $out if encoderPath( $ENCODER{$out} );
    }
    return undef;
}

# The player's upsampling: its two targets and checked filter options, or undef when Upsample is
# off or cannot run for it (no ffmpeg with soxr, no hook, no known max: Upsample::available).
# The targets stop at the cap for its output (768k with WAV out, 384k with FLAC) or at the max rate
# chosen, whichever is lower (Upsample::cap), so the marker's ut= records a change of either.
# The intermediate and minimum phases need sox; without it the player gets linear. A fixed target
# is both families' target, the player's top rate (Upsample::top), so the marker's ut= records it.
# $out: the output the targets are for ('WAV' or 'FLAC'), the player's own when not given. A
# PCM-mode player's FLAC rules ask for 'FLAC'; their targets follow from what ut= records.
# A reduced stream (MP3, Opus, Ogg) is never upsampled: undef.
sub upsampleSpec {
    my ( $client, $prefs, $out ) = @_;
    return undef unless defined $prefs->{Upsample} && $prefs->{Upsample} eq 'on';
    $out //= outputFormat( $client, $prefs );
    return undef if isReduced($out);
    my %o = Plugins::SqueezeDSP::Upsample::options($prefs);
    my ( $ok, undef, $f44, $f48 ) = Plugins::SqueezeDSP::Upsample::available( $client,
        Plugins::SqueezeDSP::Upsample::cap( $out, $o{max} ) );
    return undef unless $ok;
    $o{phase} = 'linear' unless Plugins::SqueezeDSP::Upsample::soxPath();
    $f44 = $f48 = Plugins::SqueezeDSP::Upsample::top( $f44, $f48 ) if $o{target} eq 'fixed';
    return { f44 => $f44, f48 => $f48, %o };
}

# What a player can take, asked of its own list, not of the one applyFormatOrder set:
#   'dsp': 24-bit FLAC. No DSP below it (Simon, 2026-10-24): in LMS 9.1 that leaves out
#          SliMP3 and the Squeezebox 1; SoftSqueeze is left out by name, as upstream did. An
#          app that could play it but asks for a lossy format takes it too, reduced (_lossyApp).
#   'wav': a WAV header in a pcm stream. A squeezelite started with -W reads one and lists
#          wav (pcm.c:456-485); without it, the strm announce rules.
sub playerTakes {
    my ( $client, $what ) = @_;
    my @list = nativeFormats($client);
    return _canDsp( $client, \@list ) if $what eq 'dsp';
    return ( grep { $_ eq 'wav' } @list ) ? 1 : 0 if $what eq 'wav';
    return 0;
}

# The player's own format list, as its HELO gave it. While DSP is on, LMS sees the list
# applyFormatOrder set, and the player's own is kept in pluginData.
sub nativeFormats {
    my $client = shift;
    return $client->formats() unless $client->can('myFormats') && ref $client->myFormats eq 'ARRAY';

    my $current = $client->myFormats;
    my $saved   = $client->pluginData('origFormats');
    my $applied = $client->pluginData('appliedFormats');
    # a list other than the one we last set came from a HELO: it is the player's own
    return ( ref $saved eq 'ARRAY' && ref $applied eq 'ARRAY' && "@$current" eq "@$applied" ) ? @$saved : @$current;
}

# Every format a SqueezeDSP rule takes as input, and pcm. With prioritizeNative on (LMS's
# default, Prefs.pm:205), getConvertCommand2 first moves the source's own format to the
# front of the player's list, wherever it sits (TranscodingHelper.pm:359-366), and pcm
# first for a wav source (:361); LMS's rule there is the player's native passthrough.
# So a DSP-on player lists none of these but its output format.
my %_covered;
sub coveredFormats {
    unless ( %_covered ) {
        for ( split /\n/, Plugins::SqueezeDSP::TemplateConfig::template_FLAC24() ) {
            $_covered{$1} = 1 if /^(\S+)\s+\S+\s+\$MODEL\$\s+\$CLIENTID\$\s*$/;
        }
        $_covered{pcm} = 1;
    }
    return sort keys %_covered;
}

# The third field of a rule header. getConvertCommand2 tries <model>-<id> before *-<id>
# (TranscodingHelper.pm:373-378), so a rule here beats another plugin's per-player rule
# (DSDPlayer writes dsf/dff -> flc as * <id>). The header splits on whitespace and loses
# anything after a '#', so an odd model falls back to '*'.
sub modelSlot {
    my $client = shift;
    my $model = defined $client ? ( $client->model() // '' ) : '';
    return $model =~ /^[A-Za-z0-9_.+-]+$/ ? $model : '*';
}

# DSD is decoded to DXD (352.8k), or to the player's top 44.1k-family rate below that.
# An unknown maximum caps nothing, as in LMS's own samplerateLimit. With Upsample on ($up,
# upsampleSpec's), DSD goes where a 44.1k-family track goes: its family's target, or the fixed
# target, so the player keeps one rate. Up to the 384k cap that is the rate above (DXD, or the
# player's top 44.1k-family rate); with WAV out it follows the 768k cap to 705.6k, and a max
# rate chosen lowers it as it lowers every track. A DSF gets there through our engine's own
# decoder (dsdEngine); a DFF through ffmpeg's soxr when that ffmpeg has soxr (Upsample::dsdFilter),
# as it takes DSD128 and above down to DXD; without soxr, ffmpeg's own resampler does. A reduced stream ($out MP3, OPS, OGG) takes DSD to 44.1k: its
# encoder works at 48k or under (Vorbis at the rate it is given), so a higher rate would only
# cost the DSP time.
sub dxdTarget {
    my ( $client, $up, $out ) = @_;
    return 44100 if isReduced($out);
    return $up->{f44} if $up;
    my $max = ( defined $client && $client->can('maxSupportedSamplerate') ) ? ( $client->maxSupportedSamplerate() || 0 ) : 0;
    my $target = 352800;
    return $target unless $max;
    $target /= 2 while $target > $max && $target > 44100;
    return $target;
}

# Our engine decodes a DSF itself (TemplateConfig _dsdEngine) at every DSD target this plugin gives: 44.1k to 768k in
# either family (ledger OUR OWN DSD DECODER). A 48k-family target (Upsample's fixed 96k, 384k or 768k) and 705.6k from
# DSD64 take the decoder's stage 3 (STAGE 3: THE RATIONAL STAGE). The target is in the conf's marker (dxd=), so a
# change of target rewrites the rules.
sub dsdEngine {
    my $t = dxdTarget(@_);
    return ( $t && $t <= 768000 && ( $t % 44100 == 0 || $t % 48000 == 0 ) ) ? 1 : 0;
}

# Bring LMS's live transcode table in step with the conf just written. A reload cannot:
# loadConversionTables never empties the table and stores a repeated profile again as
# <profile>-N (TranscodingHelper.pm:51, :101), so a removed rule would stay live until
# restart. Instead: drop every rule that runs our engine, then add the conf's rules,
# read exactly as loadConversionTables reads a file (:79-130). LMS's native rules and
# other plugins' rules are never touched.
sub syncCommandTable {
    my $path = shift;
    my $table = \%Slim::Player::TranscodingHelper::commandTable;
    my $caps  = \%Slim::Player::TranscodingHelper::capabilities;
    my $tag   = '[' . $Plugins::SqueezeDSP::Plugin::convolver . ']';

    my $getCapabilities = Slim::Player::TranscodingHelper->can('_getCapabilities') or do {
        Plugins::SqueezeDSP::Utils::fatal( undef, undef, "LMS has no _getCapabilities: DSP changes apply after a restart" );
        return 0;
    };

    open( my $fh, '<', $path ) or do {
        Plugins::SqueezeDSP::Utils::fatal( undef, undef, "Can't read $path to update the transcode table" );
        return 0;
    };
    my @lines = <$fh>;
    close( $fh );

    my $dropped = 0;
    for my $profile ( grep { index( $table->{$_} // '', $tag ) >= 0 } keys %$table ) {
        delete $table->{$profile};
        delete $caps->{$profile};
        $dropped++;
    }

    my $added = 0;
    for my $rule ( _readRules( \@lines, $tag ) ) {
        my ( $profile, $capLine, $command ) = @$rule;

        # a repeated profile is the next instance, tried only when the one before is rejected
        my $key = $profile;
        if ( exists $table->{$key} ) {
            my $n = 1;
            $n++ while exists $table->{"$profile-$n"};
            $key = "$profile-$n";
        }
        if ( defined $capLine ) {
            $getCapabilities->( $key, $capLine );
        } else {
            $caps->{$key} = { I => 'noArgs', F => 'noArgs' };   # LMS's default capabilities
        }
        $table->{$key} = $command;
        $added++;
    }

    Plugins::SqueezeDSP::Utils::debug( "Transcode table updated: $dropped SqueezeDSP rules dropped, $added added" );
    return 1;
}

# Our rules in conf lines, in order, read as loadConversionTables reads them (TranscodingHelper.pm:79-130):
# [ profile, capabilities line (undef when the rule has none), command ]. A repeated profile is returned as
# it stands; the caller numbers the instances.
sub _readRules {
    my ( $lines, $tag ) = @_;
    my @lines = @$lines;
    my @rules;
    while ( defined( my $line = shift @lines ) ) {
        next if $line =~ /^\s*#/;
        next if $line =~ /^\s*$/;
        $line =~ s/#.*$//;
        $line =~ s/^\s*//;
        $line =~ s/\s*$//;
        next unless $line =~ /^(\S+)\s+(\S+)\s+(\S+)\s+(\S+)$/;
        my $profile = "$1-$2-$3-" . lc($4);

        # the line after a header is the capabilities line or the command, read as is
        my $next = shift @lines;
        my $capLine;
        if ( defined $next && $next =~ /^\s+#\s+(\S.*)/ ) {
            $capLine = $1;
            $next = shift @lines;
        }
        my $command = $next // '';
        $command =~ s/^\s*//;
        $command =~ s/\s*$//;
        next unless length($command) && index( $command, $tag ) >= 0;
        push @rules, [ $profile, $capLine, $command ];
    }
    return @rules;
}

# The rule this player would have with Upsample off, standing in for one of its Upsample rules: the same
# input type and output, the first rule without D that takes the stream mode LMS chose (a track at its
# target is within the max, so it needs no D). [ capabilities as LMS parses them, command ], or undef.
# Upsample::_tokenize hands it to LMS for a track already at its target rate, so no resampler runs for it
# (Simon 2026-10-25: "No need to feed material into it that is already the highest rate").
sub plainRule {
    my ( $client, $profile, $mode ) = @_;
    return undef unless defined $client && defined $profile && defined $mode && $mode =~ /^[FIR]$/;
    my ( $in, $out ) = split /-/, $profile;
    return undef unless defined $out;
    my $state = clientState($client);
    return undef if $state eq STATE_OFF;
    my $fmt = $out eq 'wav' ? 'WAV' : ( $state =~ /\bpath=engine\b/ ) ? 'FLACENGINE' : 'FLAC24';
    my $tag = '[' . $Plugins::SqueezeDSP::Plugin::convolver . ']';
    my $text = template( $client->id, $fmt, modelSlot($client), undef );
    for my $rule ( _readRules( [ split /^/m, $text ], $tag ) ) {
        my ( $key, $capLine, $command ) = @$rule;
        next unless index( $key, "$in-$out-" ) == 0;
        my $caps = _capabilities($capLine) or return undef;
        next if exists $caps->{D} || !exists $caps->{$mode};
        return [ $caps, $command ];
    }
    return undef;
}

# A capabilities line parsed by LMS's own _getCapabilities, into a key of its table that exists only for
# the call; no line: LMS's defaults, as syncCommandTable gives them
sub _capabilities {
    my $line = shift;
    return { I => 'noArgs', F => 'noArgs', E => {} } unless defined $line;
    my $get = Slim::Player::TranscodingHelper->can('_getCapabilities') or return undef;
    my $key = ' SqueezeDSP plain rule';
    local $Slim::Player::TranscodingHelper::capabilities{$key};
    $get->( $key, $line );
    return $Slim::Player::TranscodingHelper::capabilities{$key};
}

# Every change of a player's DSP state comes through here: settings saved, a preset
# loaded, the player connected. The conf and the live table are rewritten only when a
# state moved; the format order is set every time, because a HELO resets it.
sub applyClientState {
    my $client = shift;
    return unless defined $client;
    initConfiguration($client);
    applyFormatOrder( $client, clientState($client) );
    warnMixedSync($client);
}

# Every format must reach the DSP (Simon, 2026-10-24). getConvertCommand2 walks the
# player's formats in order (TranscodingHelper.pm:371), and with prioritizeNative on it
# first moves the source's own format to the front (:359-366), so a covered format
# anywhere in the list reaches LMS's native rule ahead of ours. A DSP-on player's list is
# therefore its output format, then only formats no SqueezeDSP rule takes. DSP off puts
# the player's own list back. Players with a fixed list (no myFormats) keep it, logged.
sub applyFormatOrder {
    my ( $client, $state ) = @_;
    my $on = $state ne STATE_OFF;
    # WAV mode keeps flc after wav: a track above the max then still has our FLAC rule, and
    # a sync group of this player and one without wav still shares a format. A reduced stream
    # leads with its own format alone: its rules take every type, within the max or above it.
    my ($out) = $state =~ /\bout=(\w+)/;
    my @lead = isReduced($out) ? ( reducedStream($out) ) : ( $out // '' ) eq 'WAV' ? ( 'wav', 'flc' ) : ( 'flc' );
    my %covered = map { $_ => 1 } coveredFormats();

    unless ( $client->can('myFormats') && ref $client->myFormats eq 'ARRAY' ) {
        if ( $on ) {
            my @list = $client->formats();
            my %lead = map { $_ => 1 } @lead;
            my $pn   = Slim::Utils::Prefs::preferences('server')->get('prioritizeNative');
            # a covered format listed before ours wins; with prioritizeNative, any of them
            my ($at) = grep { $lead{ $list[$_] } } 0 .. $#list;
            my @bypass = map { $list[$_] }
                grep { $covered{ $list[$_] } && !$lead{ $list[$_] } && ( $pn || !defined $at || $_ < $at ) } 0 .. $#list;
            my $msg = $client->name . " has a fixed format list (" . join( ',', @list ) . "), left as is";
            if ( @bypass ) {
                $Plugins::SqueezeDSP::Plugin::log->warn( "$msg: " . join( ',', @bypass ) . " sources bypass the DSP"
                    . ( $pn ? " (prioritizeNative is on)" : "" ) );
            } else {
                Plugins::SqueezeDSP::Utils::debug( $msg );
            }
        }
        return;
    }

    my $current = $client->myFormats;
    my @orig = nativeFormats($client);

    my @want = @orig;
    if ( $on ) {
        my %has   = map { $_ => 1 } @orig;
        my @front = grep { $has{$_} } @lead;
        my %front = map { $_ => 1 } @front;
        @want = ( @front, grep { !$front{$_} && !$covered{$_} } @orig );
    }

    $client->pluginData( 'origFormats', [ @orig ] );
    $client->pluginData( 'appliedFormats', [ @want ] );
    return if "@want" eq "@$current";

    $client->myFormats( [ @want ] );
    Plugins::SqueezeDSP::Utils::debug( $client->name . " formats: " . join( ',', @want ) );
}

# A sync group plays one stream, built from the master's rule (TranscodingHelper.pm:320),
# so a member with another DSP state hears the master's. Logged, not changed.
sub warnMixedSync {
    my $client = shift;
    return unless defined $client && $client->isSynced(1);

    my @members = $client->syncGroupActiveMembers();
    my %on = map { $_->id() => ( clientState($_) ne STATE_OFF ? 1 : 0 ) } @members;
    my %states = map { $_ => 1 } values %on;
    return unless keys %states > 1;

    my $master = $client->master();
    $Plugins::SqueezeDSP::Plugin::log->warn( "Sync group " . join( ', ', map { $_->name } @members )
        . " mixes DSP on and off: every member hears the master's (" . $master->name . ", DSP "
        . ( $on{ $master->id() } ? 'on' : 'off' ) . ")" );
}


# Going to run this each time the server starts and update revision number for each active client
sub upgradePrefs
{
	# Going to run this each time the server starts and update revision number for each active client
	# 
	my (  @clientIDs ) = @_;
	$Plugins::SqueezeDSP::Plugin::revision;
		
	foreach my $clientID ( @clientIDs )
	{
		Plugins::SqueezeDSP::Utils::debug( "client " . $clientID );
		my $client = Slim::Player::Client::getClient( $clientID );
	# Check IMMEDIATELY after getClient()
    unless (defined($client))
		{  # Or if (not defined $client) {
        # a player named in the conf but not connected; this runs on every settings
        # save now, so it goes to the debug log rather than to STDOUT
        Plugins::SqueezeDSP::Utils::debug( "No client object for ID: " . $clientID . " (not connected)" );
        next; # Skip to the next client ID
    	}
		# Check the current and previous installed versions of the plugin,
		# in case we need to upgrade the prefs store (for this client)
		
		my $myJSONFile = Plugins::SqueezeDSP::Utils::getPrefFile( $client );
		
		unless( -f $myJSONFile )
		{
			#create default file if it does not exist
			Plugins::SqueezeDSP::Utils::debug( $client,  "Player Config File $myJSONFile not found. Creating" );
			#need to create file first
			my $myDefault = my $myDefault = {
				Client     => { Bypass => "1", OutputFormat => "FLAC" },
				ClientName => "",
				Revision   => 0
			};
			Plugins::SqueezeDSP::Utils::SaveJSONFile ($myDefault, $myJSONFile );
			#Plugins::SqueezeDSP::Utils::defaultPrefs( $client );
		}

		my $myConfig = Plugins::SqueezeDSP::Utils::LoadJSONFile ($myJSONFile);
		Plugins::SqueezeDSP::Utils::debug ( $clientID . " Config File " . $myJSONFile . " loaded" );
		my $prevrev = $myConfig->{Client}->{Version};
	
		#Now touch the version numbers	
		if( defined( $client ) )
		{
			if ( $prevrev eq $Plugins::SqueezeDSP::Plugin::revision )
			{
				Plugins::SqueezeDSP::Utils::debug ( "upgrade from " . $prevrev . " not required " )	;
			}
		
			else   
			{
				Plugins::SqueezeDSP::Utils::debug( "upgrade from " . $prevrev . " to " . $Plugins::SqueezeDSP::Plugin::revision );
				Plugins::SqueezeDSP::Utils::setPref( $client, "Version", $Plugins::SqueezeDSP::Plugin::revision );
			
			}
		}
	}
}


# find the appropriate template depending on client type; $up is upsampleSpec's, or undef
sub template {
    my ($clientID, $format, $model, $up) = @_;
    $model = '*' unless defined $model && length $model;
    my $client = Slim::Player::Client::getClient($clientID);

    my $source = Plugins::SqueezeDSP::TemplateConfig->can( "template_$format" ) or do {
        Plugins::SqueezeDSP::Utils::fatal( $client, 'template', "no conf template $format" );
        return '';
    };
    # linear phase is ffmpeg's soxr, intermediate and minimum sox's rate (Upsample.pm @PHASES); DSD is decoded by
    # DSDPlayer's dsdplay or LMS's sox where the server has no ffmpeg; MP3 at a Bitrate Limiting is encoded at it
    my $sox = $up && $up->{phase} ne 'linear';
    my $dsd = dsdTool();
    my %opts = ( dsd => ( $dsd eq 'dsdplay' || $dsd eq 'sox' ) ? $dsd : 'ffmpeg', dsdEngine => dsdEngine( $client, $up, $format ) );
    $opts{kbps} = mp3Limit( $client, $format ) if defined $client;
    my $template = $source->( !$up ? 0 : $sox ? 2 : 1, \%opts );

    # the target is picked per track at stream start (Upsample::_tokenize); the filter options are the player's
    if ( $up ) {
        my $rate = Plugins::SqueezeDSP::Upsample::PLACEHOLDER() . "$up->{f44}_$up->{f48}";
        my $opts = $sox ? Plugins::SqueezeDSP::Upsample::soxOptions($up) : Plugins::SqueezeDSP::Upsample::ffOptions($up);
        $template =~ s/\$UPRATE\$/$rate/g;
        $template =~ s/\$UPOPTS\$/$opts/g;
        $template =~ s/\$UPSOX\$/$opts/g;
    }
    
    # Substitute placeholders (same as before)
    my $pipeout;
    my $pipenul;
    if (Slim::Utils::OSDetect::OS() eq 'win') {
        $pipeout = '#PIPE#';
        $pipenul = '';
    } else {
        $pipeout = '/dev/fd/4';
        $pipenul = '4>&1 1>/dev/null';
    }
    
    my $dxd = dxdTarget( $client, $up, $format );
    my $dxdaf = Plugins::SqueezeDSP::Upsample::dsdFilter( $dxd, $up );

    $template =~ s/\$CONVAPP\$/$Plugins::SqueezeDSP::Plugin::convolver/g;
    $template =~ s/\$MODEL\$/$model/g;
    $template =~ s/\$DXDAF\$ /$dxdaf/g;
    $template =~ s/\$DXD\$/$dxd/g;
    $template =~ s/\$CLIENTID\$/$clientID/g;
    $template =~ s/\$PIPEOUT\$/$pipeout/g;
    $template =~ s/\$PIPENUL\$/$pipenul/g;
    
    return $template;
}

1;
