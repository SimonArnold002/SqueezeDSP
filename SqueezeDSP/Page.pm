package Plugins::SqueezeDSP::Page;
# The SqueezeDSP page (docs/fork-plan.md, round 2): one player's signal path, its processing
# speed, and the controls that decide how it is fed (DSP, output, Upsample, preamp, balance,
# width, presets). The equaliser, filters and the rest stay on the classic page, linked from here.
#
# A RAW HANDLER, so the bytes below are the whole document: no template and no skin. The classic
# page is a template inside the classic skin's settings shell, which Material then frames, and
# that shell's custom-select widgets are what showed the wrong value after a reload (ledger, THE
# DROPDOWN SHOWS ITS FIRST OPTION). Here a select is a plain select. The recipe is HQPlayer
# Bridge's Live page (Live.pm there), and its two traps hold here too:
#   - a raw handler owns its STATUS CODE and its FRAMING: LMS fills in neither, so a missing
#     code goes out as "HTTP/1.1  " and a missing Content-Length leaves the browser waiting
#     for the end of a kept-alive connection;
#   - the version is handed in at init, never fetched from Plugin.pm at request time: a page
#     module that calls into Plugin.pm without loading it dies part way through the handler.
# The page itself is a NON-interpolating heredoc with @@NAME@@ placeholders, so nothing in its
# script is Perl's before it is JavaScript's. The script and the stylesheet are static files
# beside the classic page's (HTML/EN/plugins/SqueezeDSP/live/), which LMS serves with a week's
# cache, so their URLs carry the version and the server's start time.
# Since 0.1.72.40 it also serves the engine's log as plain text at /squeezedsp/log (logText, below), the log's path
# handed in at init as a reference, as the version is.
use strict;
use warnings;

use Encode ();
use JSON::XS ();

use constant PATH => '/squeezedsp';

my $VERSION = '';
my $BOOT    = 0;
my $LOGFILE = \undef;    # a reference to the plugin's log path, read at each request (set in initPlugin)
my $TEMPDIR = \undef;    # and to its temp folder, where /squeezedsp/bench writes (0.1.72.46)
our $PAGE;    # the document, at the end of this file

# Every label the page shows, by the name its script uses
my %LABELS = (
    title       => 'PLUGIN_SQUEEZEDSP_DISPLAYNAME',
    dsp         => 'PLUGIN_SQUEEZEDSP_LIVE_DSP',
    dspHelp     => 'PLUGIN_SQUEEZEDSP_BYPASS_HELP',
    noDsp       => 'PLUGIN_SQUEEZEDSP_NO_DSP_BELOW_FLAC',
    output      => 'PLUGIN_SQUEEZEDSP_OUTPUT',
    outputHelp  => 'PLUGIN_SQUEEZEDSP_OUTPUT_HELP',
    pcmNeeds    => 'PLUGIN_SQUEEZEDSP_PCM_NEEDS_HEADER',
    reducedLimit => 'PLUGIN_SQUEEZEDSP_REDUCED_LIMIT',
    reducedApp  => 'PLUGIN_SQUEEZEDSP_REDUCED_APP',
    steep       => 'PLUGIN_SQUEEZEDSP_LIVE_STEEP',
    noMax       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_MAX',
    noHook      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_HOOK',
    noUpReduced => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_NO_REDUCED',
    targets     => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_TARGETS',
    target      => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_TARGET',
    fixed       => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_FIXED',
    max         => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_MAX',
    maxNone     => 'PLUGIN_SQUEEZEDSP_UPSAMPLE_MAX_NONE',
    dsdCutoff   => 'PLUGIN_SQUEEZEDSP_DSD_CUTOFF',
    dsdGain     => 'PLUGIN_SQUEEZEDSP_DSD_GAIN',
    dsdNone     => 'PLUGIN_SQUEEZEDSP_DSD_NONE',
    dsdOutNoDsf => 'PLUGIN_SQUEEZEDSP_DSD_OUT_NO_DSF',
    dsdOutNoReduced => 'PLUGIN_SQUEEZEDSP_DSD_OUT_NO_REDUCED',
    dsdOutMod   => 'PLUGIN_SQUEEZEDSP_DSD_OUT_MOD',
    dsdOutStandard => 'PLUGIN_SQUEEZEDSP_DSD_OUT_STANDARD',
    dsdOutAdaptive => 'PLUGIN_SQUEEZEDSP_DSD_OUT_ADAPTIVE',
    dsdOutDsp   => 'PLUGIN_SQUEEZEDSP_DSD_OUT_DSP',
    dsdOutModulator => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_OUT_MODULATOR',
    # native DSD (the native round): the DSP rate's third choice and its integrator
    dsdNative     => 'PLUGIN_SQUEEZEDSP_DSD_NATIVE',
    dsdIntegrator => 'PLUGIN_SQUEEZEDSP_DSD_INTEGRATOR',
    dsdIntLin     => 'PLUGIN_SQUEEZEDSP_DSD_INT_LIN',
    dsdIntInt     => 'PLUGIN_SQUEEZEDSP_DSD_INT_INT',
    dsdIntMin     => 'PLUGIN_SQUEEZEDSP_DSD_INT_MIN',
    dsdNativeOnly => 'PLUGIN_SQUEEZEDSP_DSD_NATIVE_ONLY',
    # the tabs and the Sample Rate Converter, PCM and DSD Processing cards (Simon 2026-11-03)
    tabDsp      => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_DSP',
    tabSrc      => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_SRC',
    tabPcm      => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_PCM',
    tabDsd      => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_DSD',
    tabPeq      => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_PEQ',
    tabConv     => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_CONV',
    tabXfeed    => 'PLUGIN_SQUEEZEDSP_LIVE_TAB_XFEED',
    src         => 'PLUGIN_SQUEEZEDSP_SRC',
    srcHelp     => 'PLUGIN_SQUEEZEDSP_SRC_HELP',
    convertTo   => 'PLUGIN_SQUEEZEDSP_SRC_CONVERT_TO',
    dsdRate     => 'PLUGIN_SQUEEZEDSP_SRC_DSD_RATE',
    filterCd    => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_CD',
    filterHiRes => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_HIRES',
    # the filter family's names (0.1.72.44): the dropdown and the signal path's row
    filterKaiser => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_KAISER',
    filterGauss => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_GAUSS',
    filterHalfBand => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_HALFBAND',
    filterSlow  => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_SLOW',
    filterNonRinging => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_NONRING',
    filterShort => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_SHORT',
    filterMedium => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_MEDIUM',
    filterLong  => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_LONG',
    filterXLong => 'PLUGIN_SQUEEZEDSP_SRC_FILTER_XLONG',
    engineUp    => 'PLUGIN_SQUEEZEDSP_LIVE_ENGINE_UP',
    pcmProc     => 'PLUGIN_SQUEEZEDSP_PCM_PROC',
    pcmProcHelp => 'PLUGIN_SQUEEZEDSP_PCM_PROC_HELP',
    dsdProc     => 'PLUGIN_SQUEEZEDSP_DSD_PROC',
    dsdProcHelp => 'PLUGIN_SQUEEZEDSP_DSD_PROC_HELP',
    dsdToPcm    => 'PLUGIN_SQUEEZEDSP_DSD_TO_PCM',
    dsdToDsd    => 'PLUGIN_SQUEEZEDSP_DSD_TO_DSD',
    decoder     => 'PLUGIN_SQUEEZEDSP_DSD_DECODER',
    encoder     => 'PLUGIN_SQUEEZEDSP_DSD_ENCODER',
    dspOffTab   => 'PLUGIN_SQUEEZEDSP_LIVE_DSP_OFF_TAB',
    dspClassic  => 'PLUGIN_SQUEEZEDSP_LIVE_DSP_CLASSIC',
    peqClassic  => 'PLUGIN_SQUEEZEDSP_LIVE_PEQ_CLASSIC',
    convClassic => 'PLUGIN_SQUEEZEDSP_LIVE_CONV_CLASSIC',
    xfeedClassic => 'PLUGIN_SQUEEZEDSP_LIVE_XFEED_CLASSIC',
    openClassic => 'PLUGIN_SQUEEZEDSP_LIVE_OPEN_CLASSIC',
    clipping    => 'PLUGIN_SQUEEZEDSP_LIVE_CLIPPING',
    clipNone    => 'PLUGIN_SQUEEZEDSP_LIVE_CLIP_NONE',
    clipYes     => 'PLUGIN_SQUEEZEDSP_LIVE_CLIP_YES',
    clipHistory => 'PLUGIN_SQUEEZEDSP_LIVE_CLIP_HISTORY',
    dsdDecoder  => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_DECODER',
    dsdLowpass  => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_LOWPASS',
    dsdNoLowpass => 'PLUGIN_SQUEEZEDSP_LIVE_DSD_NO_LOWPASS',
    preamp      => 'PLUGIN_SQUEEZEDSP_PREAMP',
    preampHelp  => 'PLUGIN_SQUEEZEDSP_PREAMP_HELP',
    balance     => 'PLUGIN_SQUEEZEDSP_BALANCE',
    balanceHelp => 'PLUGIN_SQUEEZEDSP_BALANCE_HELP',
    width       => 'PLUGIN_SQUEEZEDSP_WIDTH',
    widthHelp   => 'PLUGIN_SQUEEZEDSP_WIDTH_HELP',
    apply       => 'PLUGIN_SQUEEZEDSP_LIVE_APPLY',
    presets     => 'PLUGIN_SQUEEZEDSP_HTML_PRESETS',
    load        => 'PLUGIN_SQUEEZEDSP_LIVE_LOAD',
    saveAs      => 'PLUGIN_SQUEEZEDSP_LIVE_SAVE_AS',
    del         => 'PLUGIN_SQUEEZEDSP_LIVE_DELETE',
    delAsk      => 'PLUGIN_SQUEEZEDSP_LIVE_DELETE_ASK',
    noPresets   => 'PLUGIN_SQUEEZEDSP_LIVE_NO_PRESETS',
    presetName  => 'PLUGIN_SQUEEZEDSP_LIVE_PRESET_NAME',
    path        => 'PLUGIN_SQUEEZEDSP_LIVE_PATH',
    speed       => 'PLUGIN_SQUEEZEDSP_LIVE_SPEED',
    realtime    => 'PLUGIN_SQUEEZEDSP_LIVE_REALTIME',
    totalCpu    => 'PLUGIN_SQUEEZEDSP_LIVE_TOTAL_CPU',
    measuring   => 'PLUGIN_SQUEEZEDSP_LIVE_MEASURING',
    notInPath   => 'PLUGIN_SQUEEZEDSP_LIVE_NOT_IN_PATH',
    stopped     => 'PLUGIN_SQUEEZEDSP_LIVE_STOPPED',
    paused      => 'PLUGIN_SQUEEZEDSP_LIVE_PAUSED',
    source      => 'PLUGIN_SQUEEZEDSP_LIVE_SOURCE',
    stages      => 'PLUGIN_SQUEEZEDSP_LIVE_STAGES',
    float       => 'PLUGIN_SQUEEZEDSP_LIVE_FLOAT',
    more        => 'PLUGIN_SQUEEZEDSP_LIVE_MORE',
    back        => 'PLUGIN_SQUEEZEDSP_LIVE_BACK',
    noPlayer    => 'PLUGIN_SQUEEZEDSP_LIVE_NO_PLAYER',
    saved       => 'PLUGIN_SQUEEZEDSP_LIVE_SAVED',
    saveFailed  => 'PLUGIN_SQUEEZEDSP_LIVE_SAVE_FAILED',
    off         => 'PLUGIN_SQUEEZEDSP_LIVE_OFF',
    bypassed    => 'PLUGIN_SQUEEZEDSP_LIVE_UP_BYPASSED',
    dsdUp       => 'PLUGIN_SQUEEZEDSP_LIVE_UP_DSD',
);

sub labels { return {%LABELS} }

sub init {
    my ( $class, $version, $logfile, $tempdir ) = @_;
    $VERSION = defined $version ? $version : '';
    $LOGFILE = $logfile if ref $logfile eq 'SCALAR';
    $TEMPDIR = $tempdir if ref $tempdir eq 'SCALAR';
    $BOOT    = time();
    Slim::Web::Pages->addRawFunction( qr{^/squeezedsp/?$}, \&_handler );
    Slim::Web::Pages->addRawFunction( qr{^/squeezedsp/log(?:[/?]|$)}, \&_logHandler );
    Slim::Web::Pages->addRawFunction( qr{^/squeezedsp/bench(?:[/?]|$)}, \&_benchHandler );
    return;
}

sub _handler {
    my ( $httpClient, $response ) = @_;
    return unless $httpClient && $httpClient->connected;

    my $body = page();
    $body = Encode::encode_utf8($body) if utf8::is_utf8($body);    # octets on the wire, counted below

    $response->code(200);
    $response->content_type('text/html; charset=utf-8');
    $response->header( 'Cache-Control' => 'no-cache, no-store, must-revalidate' );
    $response->header( Pragma          => 'no-cache' );
    $response->content_length( length $body );

    Slim::Web::HTTP::addHTTPResponse( $httpClient, $response, \$body );
    return;
}

# The document: the labels in the server's language, as JSON the script reads; the version and
# the cache key for the static files
sub page {
    my %L = map { $_ => Slim::Utils::Strings::string( $LABELS{$_} ) } keys %LABELS;
    my $json = JSON::XS->new->ascii->canonical->encode( { strings => \%L, version => $VERSION } );
    $json =~ s{</}{<\\/}g;    # never a closing tag inside the script element
    my $v = _esc("$VERSION-$BOOT");
    my $html = $PAGE;
    $html =~ s/\@\@TITLE\@\@/_esc( $L{title} )/ge;
    $html =~ s/\@\@VERSION\@\@/_esc($VERSION)/ge;
    $html =~ s/\@\@V\@\@/$v/g;
    $html =~ s/\@\@DATA\@\@/$json/;
    return $html;
}

sub _esc {
    my $s = shift // '';
    $s =~ s/&/&amp;/g;
    $s =~ s/</&lt;/g;
    $s =~ s/>/&gt;/g;
    $s =~ s/"/&quot;/g;
    return $s;
}

# /squeezedsp/log (0.1.72.40, Simon: "make the logs viewable over HTTP"): the engine's log, squeezedsp.log in LMS's log
# folder, which LMS does not serve, as plain text and read-only: its last lines (lines=, default 300, at most 5000) from
# its last 2 MB (read from there, so a long log never holds the server), one player's (player=, its id or its name; the
# engine writes the player's id on each line), those holding a text (match=, any case). With a player, its gapless
# note first (Gapless.pm), as the plugin last wrote it.
our $LOG_TAIL = 2 * 1024 * 1024;

sub _logHandler {
    my ( $httpClient, $response ) = @_;
    return unless $httpClient && $httpClient->connected;

    my %q = eval { $response->request->uri->query_form };
    my $body = logText( \%q );
    $body = Encode::encode_utf8($body) if utf8::is_utf8($body);

    $response->code(200);
    $response->content_type('text/plain; charset=utf-8');
    $response->header( 'Cache-Control' => 'no-cache, no-store, must-revalidate' );
    $response->header( Pragma          => 'no-cache' );
    $response->content_length( length $body );

    Slim::Web::HTTP::addHTTPResponse( $httpClient, $response, \$body );
    return;
}

sub logText {
    my $q = shift || {};
    my $n = ( $q->{lines} // '' ) =~ /^\d+$/ ? $q->{lines} : 300;
    $n = 5000 if $n > 5000;
    $n = 1    if $n < 1;
    my $match = defined $q->{match} && length $q->{match} ? lc $q->{match} : undef;
    my $id;
    if ( defined $q->{player} && length $q->{player} ) {
        my $c = eval { Slim::Player::Client::getClient( $q->{player} ) };    # an id, an address or a name
        $id = $c ? $c->id : $q->{player};
    }
    my @out;
    if ( defined $id ) {
        my $note = eval { Plugins::SqueezeDSP::Gapless::read_note($id) };
        push @out, "# gapless note for $id: "
          . ( ref $note eq 'HASH' ? join( ' ', map { "$_=" . ( $note->{$_} // '' ) } qw(continues prev this follows next) ) : 'none' );
    }
    my $file = $$LOGFILE;
    my @lines;
    if ( defined $file && length $file && open( my $fh, '<', $file ) ) {
        binmode $fh;
        my $size = -s $fh || 0;
        if ( $size > $LOG_TAIL ) {
            seek( $fh, $size - $LOG_TAIL, 0 );
            <$fh>;    # the rest of a line cut in two
        }
        while ( my $l = <$fh> ) {
            $l =~ s/[\r\n]+\z//;
            next if defined $id    && index( $l,    $id ) < 0;
            next if defined $match && index( lc $l, $match ) < 0;
            push @lines, $l;
            shift @lines if @lines > $n;
        }
        close $fh;
    }
    else {
        push @out, '# no engine log: ' . ( defined $file && length $file ? $file : 'its path is not set' );
    }
    return join( '', map { "$_\n" } @out, @lines );
}

# /squeezedsp/bench (0.1.72.46, Simon 2026-11-06: x86 figures from the server's own CPU, with no ssh and no play test):
# the engine's own timing of its loops (SqueezeDSP --bench, about ten seconds of one core), as plain text, the last
# run's. run= starts one in the background at a lower priority (never two at once, never on Windows).
our $BENCH_STALE = 120;    # seconds: a run's part file older than this is a run that died

sub _benchHandler {
    my ( $httpClient, $response ) = @_;
    return unless $httpClient && $httpClient->connected;

    my %q = eval { $response->request->uri->query_form };
    my $body = benchText( \%q );
    $body = Encode::encode_utf8($body) if utf8::is_utf8($body);

    $response->code(200);
    $response->content_type('text/plain; charset=utf-8');
    $response->header( 'Cache-Control' => 'no-cache, no-store, must-revalidate' );
    $response->header( Pragma          => 'no-cache' );
    $response->content_length( length $body );

    Slim::Web::HTTP::addHTTPResponse( $httpClient, $response, \$body );
    return;
}

sub benchText {
    my $q   = shift || {};
    my $dir = $$TEMPDIR;
    return "# no temp folder\n" unless defined $dir && length $dir;
    my $out     = "$dir/bench.txt";
    my $part    = "$out.part";
    my $running = -e $part && time - ( stat _ )[9] < $BENCH_STALE;
    my @say;
    if ( defined $q->{run} && !$running ) {
        my $why = startBench( $part, $out );
        push @say, defined $why ? "# not started: $why" : '# started';
    }
    elsif ($running) {
        push @say, '# running';
    }
    my $last = '# none yet: /squeezedsp/bench?run=1';
    if ( open( my $fh, '<', $out ) ) {
        local $/;
        $last = <$fh> // '';
        $last =~ s/\n+\z//;
        close $fh;
    }
    return join( '', map { "$_\n" } @say, $last );
}

# startBench: the engine's --bench into part, renamed to out when it ends, through /bin/sh in the background so LMS
# never waits on it. undef when started, else why not.
sub startBench {
    my ( $part, $out ) = @_;
    return 'not on Windows' if $^O =~ /MSWin/;
    my $bin = $Plugins::SqueezeDSP::Binary::ENGINE;
    return 'no engine' unless defined $bin && -x $bin;
    system( benchCommand( $bin, $part, $out ) );
    return;
}

# benchCommand: startBench's shell command, each path quoted for /bin/sh
sub benchCommand {
    my $q = sub { ( my $s = shift ) =~ s/'/'\\''/g; "'$s'" };
    my ( $bin, $part, $out ) = map { $q->($_) } @_;
    return "(nice -n 10 $bin --bench > $part 2>&1; mv -f $part $out) </dev/null >/dev/null 2>&1 &";
}

$PAGE = <<'HTML';
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>@@TITLE@@</title>
<link rel="stylesheet" href="/material/html/font/font.css">
<script>
// Material's theme, read the way the classic skin's settings header reads it: Material records
// the choice in localStorage on this same origin. 'darker' is dark, 'auto' follows the system, a
// '-colored' or '-standard' suffix is a variant, a 'user:' theme lives under its own path.
(function () {
    var theme, col;
    try {
        theme = localStorage.getItem('lms-material::theme');
        col   = localStorage.getItem('lms-material::color');
    } catch (e) {}
    if (!theme || theme === 'darker') { theme = 'dark'; }
    if (theme === 'auto') {
        theme = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
    }
    var parts = theme.split('-'), last = parts[parts.length - 1];
    if (parts.length > 1 && (last === 'colored' || last === 'standard')) { parts.pop(); }
    var name = parts.join('-');
    var isLight = theme.indexOf('light') === 0 || theme.indexOf('/light/') >= 0;
    function css(href) { var l = document.createElement('link'); l.rel = 'stylesheet'; l.href = href; document.head.appendChild(l); }
    css(name.indexOf('user:') === 0 ? '/material/usertheme/' + name.substring(5) : '/html/css/themes/' + (isLight ? 'light' : name) + '.min.css');
    if (col) { css(col.indexOf('user:') === 0 ? '/material/usercolor/' + col.substring(5) : '/html/css/colors/' + col + '.min.css'); }
    document.documentElement.className = isLight ? 'lt' : 'dk';
}());
</script>
<link rel="stylesheet" href="/plugins/SqueezeDSP/live/sqdsp_live.css?v=@@V@@">
</head>
<body>
<div class="wrap">
  <div class="top">
    <h1>@@TITLE@@</h1>
    <span class="who" id="who"></span>
    <span class="sub">@@VERSION@@</span>
    <span class="dot" id="dot"></span>
    <span class="flash" id="flash"></span>
    <button type="button" id="back"></button>
  </div>
  <div class="err" id="err"></div>
  <div class="pick" id="pick"></div>
  <div id="app"></div>
</div>
<script>var SQDSP = @@DATA@@;</script>
<script src="/plugins/SqueezeDSP/live/sqdsp_live.js?v=@@V@@"></script>
</body>
</html>
HTML

1;
