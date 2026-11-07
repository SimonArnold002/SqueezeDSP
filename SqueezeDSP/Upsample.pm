package Plugins::SqueezeDSP::Upsample;
# Upsampling (docs/fork-plan.md step 7), per player, default off.
#
# With Upsample on (the Sample Rate Converter's PCM), a track reaches our engine at its own rate, on the route it takes
# with Upsample off, and the engine resamples it itself before the DSP (--upsample, engine foxUpsampler): to the
# highest rate of its own family the player takes, by its own two-stage filters, the player's choice for a source at
# 48 kHz and under and another for hi-res (--upfilter; Simon 2026-11-04: "Separate for PCM", the converter to DSD has
# its own). In 64-bit float from the decoder on, so an inter-sample peak reaches the DSP as a real sample, its meters
# report it, and the player's Preamp gives the headroom (ledger UPSAMPLE PLACEMENT). A track above the target goes down
# the same way. Until 0.1.72.37 ffmpeg's soxr resampled before the engine (sox's rate for the intermediate and minimum
# phases, which clips inside rate: ledger SOX CLIPS INSIDE RATE), with the PCM tab's precision, passband and steep
# options; those went with it.
#
# The rules carry the target as SQDSPUP_<44.1k family>_<48k family>, written per player from its
# max. A conf rule cannot pick the family per track (its only rate selector is D, and a conf line
# does no arithmetic), so tokenizeConvertCommand2 is wrapped to fill it in at stream start: the
# plugin's only hook into LMS core. The same wrap states PCM mode's WAV length (WavLength.pm).
#
# The target (@TARGETS): 'family', each track to the top rate of its own family (the default), or
# 'fixed', every track to the player's top rate, so the player never changes rate between tracks
# (Simon 2026-10-25: "upsample to a fixed single rate max rate being that of what the dac reports back
# to LMS"). A fixed target is written as both families' (SQDSPUP_384000_384000), so the pick at stream
# start needs nothing new; DSD follows it too (Configuration.pm dxdTarget): our engine's decoder for a DSF,
# ffmpeg (through soxr where it has it: dsdFilter) for a DFF.
use strict;
use Slim::Utils::Misc;
use Slim::Player::TranscodingHelper;

# Rates above 384k are not offered with FLAC out until the rig's FLAC encoders are measured above 655,350 Hz.
use constant CAP => 384000;
# WAV out (PCM mode) has no encoder in the way, and its player reads the rate from the header, past LMS's own
# rate table, which stops at 384k (Squeezebox.pm pcm_sample_rates). 768k is the top rate any player here takes
# (our squeezelite on the Eversolo DMP-A8); nothing above it has been tried (Simon 2026-11-01: "the everolo
# supports upto 768 PCM").
use constant CAP_WAV => 768000;
use constant PLACEHOLDER => 'SQDSPUP_';

# The max rates the page offers below the player's own top rate, 0 being the player's own (Simon 2026-11-01:
# "an option in SDP to set a max rate for the fixed rate and not just go with the highest it says. We need options
# to get round any performance issues"). A max caps both families: 384k gives 352.8k / 384k, or 384k fixed.
my @MAXRATES = ( 768000, 384000, 192000, 96000 );

my @TARGETS = qw(family fixed);

# The Sample Rate Converter's DSD rates (Upsample 'dsd': every track to DSD, TemplateConfig template_DSFUP), the
# player's UpsampleDSDRate; DSD64 by default, the rate every DSD DAC takes. No player reports the DSD rates it takes
# (LMS reads only its MaxSampleRate and its formats), so it is the user's choice (Simon 2026-11-03: "purely a user
# choice based on what the dac and player can handle").
my @DSDRATES = ( 2822400, 5644800, 11289600, 22579200 );
use constant DEFAULT_DSDRATE => 2822400;

# The engine's filters (foxUpsampler): a track reaches the engine at its own rate and the engine resamples it itself,
# for the converter to DSD to 705.6k (0.1.72.33: TemplateConfig $CONVRATE; Simon 2026-11-04: "for PCM to DSD we do it on
# the high rate after resampling before we push it to the SDM") and with Upsample on to the PCM target (0.1.72.37).
# The filter is the player's choice, one for a source at 48 kHz and under and one for hi-res, each short or long, then
# linear, intermediate or minimum phase (Simon: "linear intermediate, minimun and offer short and long versions", "want
# use choice for the filters with defaults set to what we consider best": short intermediate for CD rates, short linear
# for hi-res; ledger STAGE 1 STUDY): the converter to DSD's UpsampleFilterCD and UpsampleFilterHiRes, PCM's own
# UpsamplePCMFilterCD and UpsamplePCMFilterHiRes (Simon 2026-11-04: "Separate for PCM"), with the same defaults. The
# filter sits at the lower rate's Nyquist, going up or down. Neither ffmpeg nor soxr is needed for either.
# Since 0.1.72.44 a family of filters, picked from one dropdown by name, length and phase (Simon 2026-11-06, ledger THE
# FILTER FAMILY): Kaiser (short, long, xlong: its names as before, so a stored choice carries over), Gaussian
# (gauss-short, -medium, -long), Half-band and Slow roll-off (leaky, for listening), each linear, intermediate or
# minimum, and Non-ringing (nonring), alone. The engine's Choices(), in its order; Status and TemplateConfig check
# the same names.
my @FILTERS = (
    ( map { my $l = $_; map { "$l-$_" } qw(lin int min) } qw(short long xlong) ),
    ( map { my $l = $_; map { "gauss-$l-$_" } qw(lin int min) } qw(short medium long) ),
    ( map { my $f = $_; map { "$f-$_" } qw(lin int min) } qw(halfband slow) ),
    'nonring',
);
sub filterNames { return @FILTERS }
use constant DEFAULT_FILTER_CD    => 'short-int';
use constant DEFAULT_FILTER_HIRES => 'short-lin';

# The player's two filters as the engine's --upfilter takes them, "<CD>,<hi-res>": one the page offers, else the default.
# filters: the converter to DSD's; pcmFilters: Upsample's (PCM).
sub filters    { return _filters( shift, 'UpsampleFilterCD', 'UpsampleFilterHiRes' ) }
sub pcmFilters { return _filters( shift, 'UpsamplePCMFilterCD', 'UpsamplePCMFilterHiRes' ) }

sub _filters {
    my ( $prefs, $cd, $hi ) = @_;
    $prefs ||= {};
    my @f = map {
        my $v = lc( $prefs->{ $_->[0] } // '' );
        ( grep { $_ eq $v } @FILTERS ) ? $v : $_->[1]
    } ( [ $cd, DEFAULT_FILTER_CD ], [ $hi, DEFAULT_FILTER_HIRES ] );
    return join( ',', @f );
}

our $INSTALLED = 0;
my $orig;

# ffmpeg as LMS's [ffmpeg] finds it (findbin: LMS's own Bin folders first, then the PATH),
# and whether that binary has soxr: DSD's decoder where our engine's is not (a DFF), and its
# resampler (dsdFilter). undef until checked, then cached for the server's life: a newly
# installed ffmpeg needs a restart, as LMS's own binary cache does.
our $FFMPEG;
our $SOXR;

# The highest 44.1k-family and 48k-family rates at or below the player's max and $cap (cap(): CAP
# unless given), or () when the max is unknown, so no upsample rule is written for that player. A
# player whose max is below 48k sends 48k-family tracks to its top 44.1k-family rate.
sub targets {
    my ( $max, $cap ) = @_;
    return () unless defined $max && $max =~ /^\d+$/ && $max >= 44100;
    $cap = CAP unless defined $cap && $cap =~ /^\d+$/ && $cap >= 44100;
    $cap = $max if $max < $cap;
    my $f44 = 44100;
    $f44 *= 2 while $f44 * 2 <= $cap;
    my $f48 = 48000;
    $f48 *= 2 while $f48 * 2 <= $cap;
    $f48 = $f44 if $f48 > $cap;
    return ( $f44, $f48 );
}

# The cap for a player's output ('WAV' or 'FLAC', Configuration::outputFormat) and the max rate chosen
# on the page (options()'s max, 0 for none): the lower of the two.
sub cap {
    my ( $out, $max ) = @_;
    my $cap = ( $out // '' ) eq 'WAV' ? CAP_WAV : CAP;
    return ( $max && $max < $cap ) ? $max : $cap;
}

# The fixed target: the player's top rate, the higher of its two targets. That is the max it reports
# whenever the max is a standard rate up to the cap (384000 above it, 768000 with WAV out, or the max
# rate chosen); a max between standard rates gets the highest standard rate below it.
sub top {
    my ( $f44, $f48 ) = @_;
    return ( $f44 // 0 ) > ( $f48 // 0 ) ? $f44 : $f48;
}

# The target for one track, from LMS's view of its rate (the number $SAMPLERATE$ carries): the
# 44.1k family when the rate divides by 11,025 (the test CapabilitiesHelper.pm:29 uses), the 48k
# family otherwise (32k and 64k included). An unknown rate takes the 44.1k family; the track
# still plays right, since every rate it reaches is resampled properly.
sub pick {
    my ( $src, $f44, $f48 ) = @_;
    return ( defined $src && $src =~ /^\d+$/ && $src && $src % 11025 ) ? $f48 : $f44;
}

sub fill {
    my ( $command, $src ) = @_;
    $command =~ s/SQDSPUP_(\d+)_(\d+)/pick( $src, $1, $2 )/ge;
    return $command;
}

# The player's rate options from its settings, checked: they end up inside a shell command, so only the values below
# ever get there. Target: one of @TARGETS, else family. Max: one of @MAXRATES, else 0 (the player's own top rate). DSD
# rate (the converter to DSD): one of @DSDRATES, else DSD64. The filters: filters and pcmFilters.
sub options {
    my $prefs = shift || {};
    my $t = defined $prefs->{UpsampleTarget} ? lc "$prefs->{UpsampleTarget}" : '';
    $t = 'family' unless grep { $_ eq $t } @TARGETS;
    my $m = $prefs->{UpsampleMax};
    $m = ( defined $m && "$m" =~ /^\d+$/ && grep { $_ == $m } @MAXRATES ) ? 0 + $m : 0;
    my $d = $prefs->{UpsampleDSDRate};
    $d = ( defined $d && "$d" =~ /^\d+$/ && grep { $_ == $d } @DSDRATES ) ? 0 + $d : DEFAULT_DSDRATE;
    return ( target => $t, max => $m, dsdrate => $d );
}

# The Sample Rate Converter's mode, from the player's Upsample (Simon 2026-11-03: "we choose either DSD or PCM here"):
# 'on' is PCM (every track to its PCM target, as before), 'dsd' every track to DSD at the DSD rate, anything else off
# (each track goes out as it is: PCM at its own rate, a DSF as the player's DSD output says, Configuration::dsdOut)
sub mode {
    my $v = lc( ( shift || {} )->{Upsample} // '' );
    return $v eq 'on' ? 'pcm' : $v eq 'dsd' ? 'dsd' : 'off';
}

# DSD's resampler where ffmpeg decodes it (a DFF; TemplateConfig.pm $DSD): the ffmpeg that decodes DSD takes it to its
# target ($rate, Configuration.pm dxdTarget) through soxr when this ffmpeg has soxr (Simon 2026-10-26: "we should look to
# use ffmpeg soxr then for better quality"): precision 28 (very high, computed in 64-bit above 20 bits, soxr.c:439),
# soxr's own passband, linear phase (ffmpeg hands soxr only a quality number, libswresample/soxr_resample.c:48), in
# 64-bit float throughout (internal_sample_fmt=dblp). Until 0.1.72.37 Upsample's PCM options stood in for these while it
# was on; they went with soxr's PCM route. Without soxr, '': ffmpeg's own resampler (its -ar) takes DSD there, as
# before. The trailing space is the template's, so the rule without soxr is the old one.
sub dsdFilter {
    my $rate = shift;
    return '' unless soxr();
    return "-af aresample=$rate:resampler=soxr:precision=28:cheby=0:internal_sample_fmt=dblp ";
}

sub ffmpegPath { soxr(); return $FFMPEG }

sub soxr {
    return $SOXR if defined $SOXR;
    $FFMPEG = eval { Slim::Utils::Misc::findbin('ffmpeg') } || '';
    my $conf = $FFMPEG ? ( eval { _buildconf($FFMPEG) } // '' ) : '';
    $SOXR = ( $conf =~ /--enable-libsoxr\b/ ) ? $FFMPEG : '';
    return $SOXR;
}

sub _buildconf {
    my $bin = shift;
    return scalar `"$bin" -hide_banner -buildconf 2>&1`;
}

# Whether this player can upsample, and if not why: ('', 'hook', 'nomax'), then its targets under $cap (cap(); CAP
# unless given). The engine resamples, so neither ffmpeg nor soxr is needed (until 0.1.72.37: 'noffmpeg', 'nosoxr').
sub available {
    my ( $client, $cap ) = @_;
    return ( 0, 'hook' ) unless $INSTALLED;
    my $max = ( defined $client && $client->can('maxSupportedSamplerate') ) ? $client->maxSupportedSamplerate() : 0;
    my @t = targets( $max, $cap );
    return ( 0, 'nomax' ) unless @t;
    return ( 1, '', @t );
}

# Wrap tokenizeConvertCommand2 (TranscodingHelper.pm:517). Both callers call it fully qualified
# (Song.pm:577, Web/HTTP.pm:2792), so the wrap reaches them. It touches only a command carrying the
# placeholder, works inside eval, and never hands the engine a placeholder: on any error the 44.1k-family
# target goes in, which still plays correctly.
#
# A track already at its target rate bypasses the resampler (Simon 2026-10-25: "No need to feed material
# into it that is already the highest rate"): LMS tokenizes, in place of the Upsample rule, the rule the
# player would have with Upsample off (Configuration::plainRule), with that rule's own capabilities, since
# LMS takes START and END from its table by the rule's profile (TranscodingHelper.pm:560-569). (The engine would pass
# such a track as it is too, since it resamples, 0.1.72.37; the plain rule keeps the flags off its command.) Both are
# swapped only for the call: LMS's transcoder and its table come back as they were, so a seek or a resume
# comes through here again and is judged the same way.
sub install {
    return 1 if $INSTALLED;
    my $code = Slim::Player::TranscodingHelper->can('tokenizeConvertCommand2') or return 0;
    $orig = $code;
    no strict 'refs';
    no warnings 'redefine';
    *{'Slim::Player::TranscodingHelper::tokenizeConvertCommand2'} = \&_tokenize;
    $INSTALLED = 1;
    return 1;
}

sub _tokenize {
    my $t = $_[0];
    my $command;
    my $plain = eval { _plain($t) };
    if ($plain) {
        no warnings 'once';
        local $Slim::Player::TranscodingHelper::capabilities{ $t->{profile} } = $plain->[0];
        local $t->{command} = $plain->[1];
        $command = $orig->(@_);
    }
    else {
        $command = $orig->(@_);
    }

    if ( defined $command && index( $command, PLACEHOLDER ) >= 0 ) {
        my $src = eval { $_[0]->{sampleRate} };
        my $out = eval { fill( $command, $src ) };
        if ( defined $out && index( $out, PLACEHOLDER ) < 0 ) {
            $command = $out;
        }
        else {
            $command =~ s/SQDSPUP_(\d+)_\d+/$1/g;
        }
    }

    # PCM mode's WAV header states the length still to play (WavLength.pm); it touches only our
    # WAV rules, and on any error the command stays as it was.
    my $wav = eval { Plugins::SqueezeDSP::WavLength::apply( $command, $_[0] ) };
    # the engine's gapless note for this stream's player (Gapless.pm): our rules only; the command is not touched
    eval { Plugins::SqueezeDSP::Gapless::streamStart( $_[0] ) };
    return defined $wav ? $wav : $command;
}

# The track's own rate when it is already at its target, from LMS's view of it (the number $SAMPLERATE$
# carries) and the targets in the rule; else 0
sub atTarget {
    my $t = shift;
    return 0 unless ref $t eq 'HASH';
    my ( $f44, $f48 ) = ( $t->{command} // '' ) =~ /SQDSPUP_(\d+)_(\d+)/ or return 0;
    my $src = $t->{sampleRate};
    return 0 unless defined $src && $src =~ /^\d+$/ && $src > 0;
    return pick( $src, $f44, $f48 ) == $src ? 0 + $src : 0;
}

# [ capabilities, command ] of the rule that stands in for this Upsample rule when the track is already at its
# target, or undef (not one of ours, not at its target, no plain rule found)
sub _plain {
    my $t = shift;
    atTarget($t) or return undef;
    my $client = Slim::Player::Client::getClient( $t->{clientid} ) or return undef;
    my $rule = Plugins::SqueezeDSP::Configuration::plainRule( $client, $t->{profile}, $t->{streamMode} ) or return undef;
    return index( $rule->[1], PLACEHOLDER ) < 0 ? $rule : undef;
}

1;
